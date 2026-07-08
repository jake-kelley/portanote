package main

// claude.go — bridge to the locally installed Claude Code CLI. A chat turn
// spawns `claude -p` in headless stream-json mode, points it back at this
// instance's own /mcp endpoint over loopback, and relays text deltas to the
// browser as Server-Sent Events. One turn runs at a time. The CLI inherits
// the user's existing login — no credentials are handled here.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// claudeMCPURL is this instance's own MCP endpoint; main sets it after the
// listener binds, so the spawned CLI calls back into the actual port.
var claudeMCPURL string

var claudeLook struct {
	once sync.Once
	path string
}

// detectedClaudeExe is the claude binary found at launch, computed once. PATH
// is tried first, then the usual install locations — a launchd-started process
// (macOS autostart, Finder) gets a minimal PATH that lacks ~/.local/bin and
// Homebrew.
func detectedClaudeExe() string {
	claudeLook.once.Do(func() { claudeLook.path = findClaude() })
	return claudeLook.path
}

// claudePath returns the claude binary the chat should spawn: the user's
// configured override if it exists, else the auto-detected one. A var so tests
// can stub availability.
var claudePath = func() string { return resolveClaudeExe() }

func findClaude() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	candidates := []string{
		filepath.Join(home, ".local", "bin", name),    // native installer default
		filepath.Join(home, ".claude", "local", name), // older "claude migrate-installer" location
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates,
			"/opt/homebrew/bin/claude", // Homebrew (Apple Silicon)
			"/usr/local/bin/claude",    // Homebrew (Intel) / npm -g
		)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

func claudeAvailable() bool { return claudePath() != "" }

// one chat turn at a time; /api/claude/stop kills the tracked process
var claudeTurn struct {
	sync.Mutex
	running bool
	stopped bool
	cmd     *exec.Cmd
}

// sseEvent is the wire format shared with the UI — do not change the shape.
type sseEvent struct {
	Type  string `json:"type"` // "delta" | "done" | "error"
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

func claudeChatHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			NoteID    string           `json:"noteId"`
			Message   string           `json:"message"`
			Selection *claudeSelection `json:"selection"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Message) == "" {
			writeErr(w, http.StatusBadRequest, errors.New("message is required"))
			return
		}
		if !claudeAvailable() {
			writeErr(w, http.StatusServiceUnavailable,
				errors.New("claude CLI not found — install Claude Code and sign in"))
			return
		}
		claudeTurn.Lock()
		if claudeTurn.running {
			claudeTurn.Unlock()
			writeErr(w, http.StatusConflict, errors.New("a chat turn is already running"))
			return
		}
		claudeTurn.running, claudeTurn.stopped, claudeTurn.cmd = true, false, nil
		claudeTurn.Unlock()
		defer func() {
			claudeTurn.Lock()
			claudeTurn.running, claudeTurn.cmd = false, nil
			claudeTurn.Unlock()
		}()

		var note *Note
		if req.NoteID != "" {
			n, err := store.Get(req.NoteID)
			if err != nil {
				writeErr(w, http.StatusNotFound, err)
				return
			}
			note = n
		}

		fl, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		emit := func(ev sseEvent) {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if fl != nil {
				fl.Flush()
			}
		}

		start := time.Now()
		err := runClaude(r.Context(), store.dir, req.Message, claudeContextPrompt(note, req.Selection), func(text string) {
			emit(sseEvent{Type: "delta", Text: text})
		})
		logClaudeTurn(note, req.Selection, req.Message, start, err)
		if err != nil {
			emit(sseEvent{Type: "error", Error: err.Error()})
			return
		}
		emit(sseEvent{Type: "done"})
	}
}

// claudeStopHandler kills the in-flight turn, if any; the chat stream then
// ends with an "error: stopped" event. 200 either way.
func claudeStopHandler(w http.ResponseWriter, r *http.Request) {
	claudeTurn.Lock()
	if claudeTurn.cmd != nil && claudeTurn.cmd.Process != nil {
		claudeTurn.stopped = true
		claudeTurn.cmd.Process.Kill()
	}
	claudeTurn.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// claudeSelection is the region the user has highlighted in the note editor,
// expanded to full 1-based lines by the UI. It pins where an edit or question
// is aimed.
type claudeSelection struct {
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Text      string `json:"text"`
}

// valid rejects ranges the UI should never send (and anything not tied to an
// open note is meaningless — line numbers need a body to count against).
func (s *claudeSelection) valid() bool {
	return s != nil && s.StartLine >= 1 && s.EndLine >= s.StartLine && strings.TrimSpace(s.Text) != ""
}

// claudeContextPrompt situates the CLI inside Portanote. Replies land in a
// small Markdown side panel, so brevity is part of the contract.
func claudeContextPrompt(n *Note, sel *claudeSelection) string {
	const rendering = "Replies render as Markdown in a small side panel — keep them concise."
	if n == nil {
		return "The user is chatting from Portanote (a local Markdown notes app) with no note open. " +
			"Use the portanote MCP tools to find, read, and edit notes. " + rendering
	}
	tags, _ := json.Marshal(n.Tags)
	prompt := fmt.Sprintf("The user is in Portanote and currently has this note open: id %q, title %q, "+
		"folder %q, tags %s. Operate on this note unless told otherwise; read its body with the "+
		"read_note tool before answering questions about it or editing it. Edits go through "+
		"update_note (body is a full replacement). "+rendering,
		n.ID, n.Title, n.Folder, tags)
	if sel.valid() {
		text := sel.Text
		if len(text) > 8<<10 { // keep the prompt bounded; the full note is a read_note away
			text = text[:8<<10] + "…"
		}
		var lines strings.Builder
		for i, l := range strings.Split(text, "\n") {
			fmt.Fprintf(&lines, "%d: %s\n", sel.StartLine+i, l)
		}
		prompt += fmt.Sprintf("\n\nThe user has highlighted lines %d-%d of the note (1-based line numbers "+
			"in the current body). Their message refers to this region — target edits and answers there "+
			"unless they say otherwise:\n%s", sel.StartLine, sel.EndLine, lines.String())
	}
	return prompt
}

// runClaude spawns one headless CLI turn and forwards its text deltas. A var
// so tests can fake the spawn; the SSE framing above is what's under test.
var runClaude = func(ctx context.Context, dir, message, sysPrompt string, emit func(string)) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// inline JSON is a single argv entry — no shell, no quoting issues
	mcpCfg := fmt.Sprintf(`{"mcpServers":{"portanote":{"type":"http","url":%q}}}`, claudeMCPURL)
	args := []string{
		"-p", message,
		"--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--mcp-config", mcpCfg,
		"--strict-mcp-config", // only our MCP server, whatever the user's own config says
		"--allowedTools", "mcp__portanote__*",
		"--disallowedTools", "Bash,Edit,Write,NotebookEdit,WebFetch,WebSearch",
		"--permission-mode", "dontAsk", // blocked tools fail fast instead of prompting
		"--append-system-prompt", sysPrompt,
	}
	if s := claudeSettingsArg(); s != "" { // only when the user set a custom settings file
		args = append(args, "--settings", s)
	}
	cmd := exec.CommandContext(ctx, claudePath(), args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(nil) // an open stdin makes the CLI sit waiting on it
	cmd.SysProcAttr = noWindowAttr()
	stderr := &limitedBuffer{max: 8 << 10}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	claudeTurn.Lock()
	claudeTurn.cmd = cmd
	claudeTurn.Unlock()

	res := parseClaudeStream(stdout, emit)
	waitErr := cmd.Wait()

	claudeTurn.Lock()
	stopped := claudeTurn.stopped
	claudeTurn.Unlock()
	switch {
	case stopped:
		return errors.New("stopped")
	case ctx.Err() == context.DeadlineExceeded:
		return errors.New("claude took longer than 5 minutes and was stopped")
	case res.sawResult && res.isError:
		if res.text == "" {
			return errors.New("claude reported an error")
		}
		return errors.New(res.text)
	case res.sawResult:
		return nil
	default:
		// died without a result line: the reason is on stderr (e.g. not logged in)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && waitErr != nil {
			msg = waitErr.Error()
		}
		if msg == "" {
			msg = "claude exited without a result"
		}
		return errors.New(msg)
	}
}

// claudeResult is the terminal state of one stream-json conversation.
type claudeResult struct {
	sawResult bool
	isError   bool
	text      string // the "result" field of the terminal line
}

// parseClaudeStream reads the CLI's newline-delimited stream-json output,
// calling emit for each streamed text fragment. Only stream_event text
// deltas are emitted — complete "assistant" messages repeat the same text
// and would double-print. Unparseable lines are skipped, not fatal.
func parseClaudeStream(r io.Reader, emit func(string)) claudeResult {
	var res claudeResult
	deltas := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20) // result lines carry the whole reply
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			Type  string `json:"type"`
			Event struct {
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			} `json:"event"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "stream_event":
			if msg.Event.Delta.Type == "text_delta" && msg.Event.Delta.Text != "" {
				emit(msg.Event.Delta.Text)
				deltas++
			}
		case "result":
			res.sawResult, res.isError, res.text = true, msg.IsError, msg.Result
		}
	}
	// a CLI that streamed nothing (older version, changed flags) still ends
	// with the full reply in the result line — surface it once
	if res.sawResult && !res.isError && deltas == 0 && res.text != "" {
		emit(res.text)
	}
	return res
}

// limitedBuffer keeps the first max bytes written and drops the rest.
type limitedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.max - b.buf.Len(); room > 0 {
		if n > room {
			p = p[:room]
		}
		b.buf.Write(p)
	}
	return n, nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
