package main

// claude_tags.go — AI tag suggestions via the local Claude Code CLI. Unlike the
// chat drawer (claude.go), a tag turn is a single non-interactive `claude -p`
// call with no MCP tools: the note and the user's whole tag vocabulary are
// inlined in the prompt, and the reply is a bare JSON array of tags. The
// offline TF-IDF suggester (search.go) stays the default; this endpoint runs
// only when the user clicks a Generate button.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

// one AI tag run at a time — the bulk action calls sequentially anyway, and a
// second concurrent CLI spawn would just fight the first for the same login
var aiTagTurn sync.Mutex

const aiTagMax = 6 // same cap as the built-in suggester

// runClaudeOnce spawns one headless CLI call and returns its final result text.
// A var so tests can fake the spawn.
var runClaudeOnce = func(ctx context.Context, dir, prompt string) (string, error) {
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--strict-mcp-config", // no MCP servers — all context is inline
		"--disallowedTools", "Bash,Edit,Write,NotebookEdit,WebFetch,WebSearch",
		"--permission-mode", "dontAsk",
	}
	if s := claudeSettingsArg(); s != "" { // only when the user set a custom settings file
		args = append(args, "--settings", s)
	}
	cmd := exec.CommandContext(ctx, claudePath(), args...)
	cmd.Dir = dir
	cmd.Env = claudeSpawnEnv()
	cmd.Stdin = bytes.NewReader(nil) // an open stdin makes the CLI sit waiting on it
	cmd.SysProcAttr = noWindowAttr()
	stderr := &limitedBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	out, runErr := cmd.Output()

	var res struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &res) != nil {
		// died without a result object: the reason is on stderr (e.g. not logged in)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && runErr != nil {
			msg = runErr.Error()
		}
		if msg == "" {
			msg = "claude exited without a result"
		}
		return "", errors.New(msg)
	}
	if res.IsError {
		if res.Result == "" {
			return "", errors.New("claude reported an error")
		}
		return "", errors.New(res.Result)
	}
	return res.Result, nil
}

// tagVocab renders every tag in use as "name (count)", most-used first, so the
// model can reuse the user's existing vocabulary instead of inventing synonyms.
func tagVocab(items []ListItem) []string {
	counts := map[string]int{}
	for _, it := range items {
		if it.Trashed {
			continue
		}
		for _, t := range it.Tags {
			counts[t]++
		}
	}
	names := make([]string, 0, len(counts))
	for t := range counts {
		names = append(names, t)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	const maxVocab = 150 // keep the prompt bounded on huge collections
	if len(names) > maxVocab {
		names = names[:maxVocab]
	}
	out := make([]string, len(names))
	for i, t := range names {
		out[i] = fmt.Sprintf("%s (%d)", t, counts[t])
	}
	return out
}

// aiTagPrompt is the whole conversation: note, current tags, vocabulary, rules.
func aiTagPrompt(n *Note, vocab []string) string {
	var b strings.Builder
	b.WriteString("You are choosing topic tags for a note in Portanote, the user's local Markdown notes app.\n\n")
	fmt.Fprintf(&b, "Note title: %q\n", n.Title)
	if n.Folder != "" {
		fmt.Fprintf(&b, "Folder: %q\n", n.Folder)
	}
	cur, _ := json.Marshal(n.Tags)
	fmt.Fprintf(&b, "Tags already on this note: %s\n\n", cur)
	if len(vocab) > 0 {
		b.WriteString("The user's existing tags, with how many notes use each:\n")
		b.WriteString(strings.Join(vocab, ", "))
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Suggest up to %d topic tags for the note body below. Rules:\n", aiTagMax)
	b.WriteString("- Strongly prefer reusing existing tags that fit the note; invent a new tag only when nothing existing applies.\n" +
		"- Match the style of the existing tags (case, hyphenation). With no vocabulary to match, use short lowercase tags.\n" +
		"- Never repeat a tag already on the note.\n" +
		"- Reply with ONLY a JSON array of strings, e.g. [\"aws\",\"networking\"] — no prose, no code fence.\n\n")
	body := n.Body
	if len(body) > 12<<10 { // keep the prompt bounded; tags come from the gist anyway
		body = body[:12<<10] + "\n…(truncated)"
	}
	b.WriteString("Note body (Markdown):\n")
	b.WriteString(body)
	return b.String()
}

// parseTagList extracts the tag array from the model's reply, tolerating a code
// fence or stray prose around it — the first substring that parses as a string
// array wins (a reply can contain more than one). Nil means "no array found"
// (an error), while an empty non-nil slice is a genuine "no suggestions".
func parseTagList(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] != '[' {
			continue
		}
		var tags []string
		// a decoder reads exactly one JSON value, so trailing prose is fine
		if json.NewDecoder(strings.NewReader(s[i:])).Decode(&tags) == nil {
			return cleanTags(tags)
		}
	}
	return nil
}

// suggestTagsAIHandler asks the claude CLI for tag suggestions for one note.
// Same response shape as the offline endpoint: {"tags": [...]}.
func suggestTagsAIHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !claudeAvailable() {
			writeErr(w, http.StatusServiceUnavailable,
				errors.New("claude CLI not found — install Claude Code and sign in"))
			return
		}
		n, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		if !aiTagTurn.TryLock() {
			writeErr(w, http.StatusConflict, errors.New("an AI tag suggestion is already running"))
			return
		}
		defer aiTagTurn.Unlock()

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		start := time.Now()
		text, err := runClaudeOnce(ctx, store.dir, aiTagPrompt(n, tagVocab(store.List())))

		var tags []string
		if err == nil {
			if tags = parseTagList(text); tags == nil {
				err = fmt.Errorf("claude did not return a tag list: %q", text)
			}
		}
		if err != nil && ctx.Err() == context.DeadlineExceeded {
			err = errors.New("claude took longer than 2 minutes and was stopped")
		}
		logClaudeTurn(n, nil, "Suggest tags (AI)", start, err)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}

		// drop tags the note already has, case-insensitively
		have := map[string]bool{}
		for _, t := range n.Tags {
			have[strings.ToLower(t)] = true
		}
		fresh := []string{}
		for _, t := range tags {
			if !have[strings.ToLower(t)] {
				fresh = append(fresh, t)
			}
		}
		if len(fresh) > aiTagMax {
			fresh = fresh[:aiTagMax]
		}
		writeJSON(w, http.StatusOK, map[string][]string{"tags": fresh})
	}
}
