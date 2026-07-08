package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// fixtures modeled on real claude 2.1.204 stream-json output (trimmed)

func streamDelta(text string) string {
	return `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + strconv.Quote(text) + `}},"session_id":"s"}`
}

func parseFixture(t *testing.T, lines []string) ([]string, claudeResult) {
	t.Helper()
	var got []string
	res := parseClaudeStream(strings.NewReader(strings.Join(lines, "\n")+"\n"),
		func(text string) { got = append(got, text) })
	return got, res
}

func TestParseClaudeStream(t *testing.T) {
	got, res := parseFixture(t, []string{
		`{"type":"system","subtype":"init","cwd":"C:\\notes","session_id":"s","tools":[]}`,
		`{"type":"system","subtype":"status","status":"requesting"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant","content":[]}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		streamDelta("Hel"),
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
		streamDelta("lo"),
		// the complete assistant message repeats the streamed text — must not double-print
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"}}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"Hello","num_turns":1}`,
	})
	if strings.Join(got, "") != "Hello" || len(got) != 2 {
		t.Errorf("deltas = %q, want [Hel lo]", got)
	}
	if !res.sawResult || res.isError || res.text != "Hello" {
		t.Errorf("result = %+v", res)
	}
}

func TestParseClaudeStreamError(t *testing.T) {
	got, res := parseFixture(t, []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"something broke"}`,
	})
	if !res.sawResult || !res.isError || res.text != "something broke" {
		t.Errorf("result = %+v", res)
	}
	if len(got) != 0 { // error text belongs in the error event, not a delta
		t.Errorf("deltas = %q, want none", got)
	}
}

func TestParseClaudeStreamNoDeltasFallback(t *testing.T) {
	got, res := parseFixture(t, []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Just the result"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"Just the result"}`,
	})
	if len(got) != 1 || got[0] != "Just the result" {
		t.Errorf("deltas = %q, want one fallback emission", got)
	}
	if !res.sawResult || res.isError {
		t.Errorf("result = %+v", res)
	}
}

func TestParseClaudeStreamGarbage(t *testing.T) {
	got, res := parseFixture(t, []string{
		`not json at all`,
		`{"type":`,
		``,
		`{}`,
		`[1,2,3]`,
		`{"type":"stream_event"}`,
	})
	if len(got) != 0 || res.sawResult {
		t.Errorf("garbage stream: deltas %q, result %+v", got, res)
	}
}

// ---------------------------------------------------------------- endpoint

// stubClaude overrides CLI discovery (and optionally the spawn seam) for one test.
func stubClaude(t *testing.T, path string, run func(context.Context, string, string, string, func(string)) error) {
	t.Helper()
	oldPath, oldRun := claudePath, runClaude
	claudePath = func() string { return path }
	if run != nil {
		runClaude = run
	}
	t.Cleanup(func() { claudePath, runClaude = oldPath, oldRun })
}

func newClaudeTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func postChat(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/claude/chat", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// sseEvents splits an SSE body into its decoded data payloads.
func sseEvents(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	for _, block := range strings.Split(strings.TrimSpace(body), "\n\n") {
		payload, ok := strings.CutPrefix(block, "data: ")
		if !ok {
			t.Fatalf("SSE frame %q lacks data: prefix", block)
		}
		var ev sseEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("bad SSE payload %q: %v", payload, err)
		}
		out = append(out, ev)
	}
	return out
}

func TestClaudeChatNotInstalled(t *testing.T) {
	stubClaude(t, "", nil)
	rec := postChat(t, claudeChatHandler(newClaudeTestStore(t)), `{"message":"hi"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var resp map[string]string
	if json.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp["error"] == "" {
		t.Errorf("want JSON error body, got %s", rec.Body.String())
	}
}

func TestClaudeChatValidation(t *testing.T) {
	stubClaude(t, "claude", func(context.Context, string, string, string, func(string)) error {
		t.Error("runClaude should not be reached")
		return nil
	})
	h := claudeChatHandler(newClaudeTestStore(t))
	if rec := postChat(t, h, `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty message: status = %d, want 400", rec.Code)
	}
	if rec := postChat(t, h, `{"message":"hi","noteId":"nope"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown note: status = %d, want 404", rec.Code)
	}
}

func TestClaudeChatBusy(t *testing.T) {
	stubClaude(t, "claude", nil)
	claudeTurn.Lock()
	claudeTurn.running = true
	claudeTurn.Unlock()
	t.Cleanup(func() {
		claudeTurn.Lock()
		claudeTurn.running = false
		claudeTurn.Unlock()
	})
	rec := postChat(t, claudeChatHandler(newClaudeTestStore(t)), `{"message":"hi"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestClaudeChatStream(t *testing.T) {
	var gotPrompt string
	stubClaude(t, "claude", func(ctx context.Context, dir, msg, sysPrompt string, emit func(string)) error {
		gotPrompt = sysPrompt
		emit("Hello ")
		emit("world")
		return nil
	})
	store := newClaudeTestStore(t)
	n, err := store.Create("Stream Test")
	if err != nil {
		t.Fatal(err)
	}
	rec := postChat(t, claudeChatHandler(store), `{"message":"hi","noteId":"`+n.ID+`"}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	evs := sseEvents(t, rec.Body.String())
	want := []sseEvent{
		{Type: "delta", Text: "Hello "},
		{Type: "delta", Text: "world"},
		{Type: "done"},
	}
	if len(evs) != len(want) {
		t.Fatalf("events = %+v, want %+v", evs, want)
	}
	for i := range want {
		if evs[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, evs[i], want[i])
		}
	}
	if !strings.Contains(gotPrompt, n.ID) || !strings.Contains(gotPrompt, "Stream Test") {
		t.Errorf("system prompt missing note context: %q", gotPrompt)
	}
}

func TestClaudeChatStreamError(t *testing.T) {
	stubClaude(t, "claude", func(ctx context.Context, dir, msg, sysPrompt string, emit func(string)) error {
		emit("partial")
		return errors.New("boom")
	})
	rec := postChat(t, claudeChatHandler(newClaudeTestStore(t)), `{"message":"hi"}`)
	evs := sseEvents(t, rec.Body.String())
	last := evs[len(evs)-1]
	if last.Type != "error" || last.Error != "boom" {
		t.Errorf("last event = %+v, want error boom", last)
	}
	for _, ev := range evs {
		if ev.Type == "done" {
			t.Error("done must not follow an error")
		}
	}
}

func TestMetaReportsClaude(t *testing.T) {
	stubClaude(t, "", nil)
	h := newAPI(newClaudeTestStore(t), fstest.MapFS{})
	req := httptest.NewRequest("GET", "/api/meta", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var meta map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if v, ok := meta["claude"].(bool); !ok || v {
		t.Errorf("meta claude = %v, want false", meta["claude"])
	}
}

// ---------------------------------------------------------------- selection

func TestClaudeContextPromptSelection(t *testing.T) {
	n := &Note{Meta: Meta{ID: "x", Title: "T"}}
	sel := &claudeSelection{StartLine: 4, EndLine: 5, Text: "alpha\nbeta"}
	p := claudeContextPrompt(n, sel)
	for _, want := range []string{"lines 4-5", "4: alpha", "5: beta"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	// invalid ranges and empty text are ignored, not injected
	for _, bad := range []*claudeSelection{
		nil,
		{StartLine: 0, EndLine: 2, Text: "x"},
		{StartLine: 3, EndLine: 2, Text: "x"},
		{StartLine: 1, EndLine: 1, Text: "   "},
	} {
		if strings.Contains(claudeContextPrompt(n, bad), "highlighted") {
			t.Errorf("selection %+v should be ignored", bad)
		}
	}
	// no open note: nothing to count lines against
	if strings.Contains(claudeContextPrompt(nil, sel), "highlighted") {
		t.Error("selection without a note should be ignored")
	}
}

func TestClaudeChatPassesSelection(t *testing.T) {
	var gotPrompt string
	stubClaude(t, "claude-fake", func(ctx context.Context, dir, msg, sys string, emit func(string)) error {
		gotPrompt = sys
		emit("ok")
		return nil
	})
	store := newClaudeTestStore(t)
	n, err := store.Create("Sel Note")
	if err != nil {
		t.Fatal(err)
	}
	h := newAPI(store, fstest.MapFS{})
	rec := postChat(t, h, `{"noteId":"`+n.ID+`","message":"fix this","selection":{"startLine":2,"endLine":3,"text":"aa\nbb"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"lines 2-3", "2: aa", "3: bb"} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("system prompt missing %q:\n%s", want, gotPrompt)
		}
	}
}
