package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// stubClaudeOnce overrides CLI discovery and the one-shot spawn seam for one test.
func stubClaudeOnce(t *testing.T, path string, run func(context.Context, string, string) (string, error)) {
	t.Helper()
	oldPath, oldRun := claudePath, runClaudeOnce
	claudePath = func() string { return path }
	if run != nil {
		runClaudeOnce = run
	}
	t.Cleanup(func() { claudePath, runClaudeOnce = oldPath, oldRun })
}

func postSuggestAI(t *testing.T, h http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/notes/"+id+"/suggest-tags-ai", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestParseTagList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`["aws","networking"]`, []string{"aws", "networking"}},
		{" [\"aws\"] \n", []string{"aws"}},
		{"```json\n[\"aws\", \"vpn\"]\n```", []string{"aws", "vpn"}},
		{`Here you go: ["k8s"] hope that helps`, []string{"k8s"}},
		{`["dup","Dup","","  "]`, []string{"dup"}}, // cleaned: dedup + trim
		{`[]`, []string{}},
		{`no array here`, nil},
		{`{"tags":["x"]}`, []string{"x"}}, // recovers the array even inside an object wrapper
		// a waffling reply with two arrays (seen in the wild) — first valid one wins
		{"[\"terraform\",\"s3\"]\n\nWait — that's wrong. Let me answer properly:\n\n[\"terraform\"]", []string{"s3", "terraform"}},
		{`["a]b","c"]`, []string{"a]b", "c"}}, // ] inside a tag string
	}
	for _, c := range cases {
		got := parseTagList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseTagList(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestSuggestTagsAINotInstalled(t *testing.T) {
	stubClaudeOnce(t, "", nil)
	store := newClaudeTestStore(t)
	n, _ := store.Create("T")
	rec := postSuggestAI(t, newAPI(store, fstest.MapFS{}), n.ID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestSuggestTagsAIUnknownNote(t *testing.T) {
	stubClaudeOnce(t, "claude", func(context.Context, string, string) (string, error) {
		t.Error("runClaudeOnce should not be reached")
		return "", nil
	})
	rec := postSuggestAI(t, newAPI(newClaudeTestStore(t), fstest.MapFS{}), "nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSuggestTagsAIHappyPath(t *testing.T) {
	loadClaudeConfig(t.TempDir())
	var gotPrompt string
	stubClaudeOnce(t, "claude", func(ctx context.Context, dir, prompt string) (string, error) {
		gotPrompt = prompt
		return `["kubernetes","Existing","networking"]`, nil
	})
	store := newClaudeTestStore(t)
	other, _ := store.Create("Other")
	store.Update(other.ID, UpdateReq{Tags: &[]string{"aws", "networking"}})
	n, _ := store.Create("K8s Runbook")
	body := "How to drain a node."
	store.Update(n.ID, UpdateReq{Body: &body, Tags: &[]string{"existing"}})

	rec := postSuggestAI(t, newAPI(store, fstest.MapFS{}), n.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string][]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	// "Existing" dropped case-insensitively; the rest come back cleaned
	want := []string{"kubernetes", "networking"}
	if !reflect.DeepEqual(resp["tags"], want) {
		t.Errorf("tags = %v, want %v", resp["tags"], want)
	}
	// the prompt carries the note and the whole tag vocabulary with counts
	for _, must := range []string{"K8s Runbook", "drain a node", "aws (1)", "networking (1)", "existing"} {
		if !strings.Contains(gotPrompt, must) {
			t.Errorf("prompt missing %q:\n%s", must, gotPrompt)
		}
	}
}

func TestSuggestTagsAIErrorsAndLog(t *testing.T) {
	loadClaudeConfig(t.TempDir())
	stubClaudeOnce(t, "claude", func(context.Context, string, string) (string, error) {
		return "", errors.New("not logged in")
	})
	store := newClaudeTestStore(t)
	n, _ := store.Create("T")
	h := newAPI(store, fstest.MapFS{})
	rec := postSuggestAI(t, h, n.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}

	// a reply with no array is an error too, and both turns land in the log
	stubClaudeOnce(t, "claude", func(context.Context, string, string) (string, error) {
		return "I cannot help with that.", nil
	})
	if rec := postSuggestAI(t, h, n.ID); rec.Code != http.StatusBadGateway {
		t.Fatalf("no-array status = %d, want 502", rec.Code)
	}

	req := httptest.NewRequest("GET", "/api/claude/logs", nil)
	lrec := httptest.NewRecorder()
	h.ServeHTTP(lrec, req)
	var logs []claudeLogEntry
	json.Unmarshal(lrec.Body.Bytes(), &logs)
	if len(logs) < 2 || logs[1].Prompt != "Suggest tags (AI)" || logs[1].OK || logs[1].Error != "not logged in" {
		t.Errorf("AI tag turns not logged: %+v", logs)
	}
}

func TestSuggestTagsAIBusy(t *testing.T) {
	stubClaudeOnce(t, "claude", nil)
	store := newClaudeTestStore(t)
	n, _ := store.Create("T")
	aiTagTurn.Lock()
	defer aiTagTurn.Unlock()
	rec := postSuggestAI(t, newAPI(store, fstest.MapFS{}), n.ID)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestSettingsAITagToggleRoundTrip(t *testing.T) {
	store := newClaudeTestStore(t)
	h := newAPI(store, fstest.MapFS{})
	put := func(body string) BackupStatus {
		req := httptest.NewRequest("PUT", "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var st BackupStatus
		json.Unmarshal(rec.Body.Bytes(), &st)
		return st
	}
	if st := put(`{"aiTagSuggestions":true}`); st.AITagSuggestions == nil || !*st.AITagSuggestions {
		t.Fatalf("toggle not stored: %+v", st.Settings)
	}
	// a PUT that omits the field (the backup Save button) must not reset it
	if st := put(`{"backupIntervalHours":5,"backupKeep":9}`); st.AITagSuggestions == nil || !*st.AITagSuggestions {
		t.Fatalf("omitted field reset the toggle: %+v", st.Settings)
	}
	if st := put(`{"aiTagSuggestions":false}`); st.AITagSuggestions == nil || *st.AITagSuggestions {
		t.Fatalf("toggle not cleared: %+v", st.Settings)
	}
	// persisted: a fresh store on the same dir sees the saved value
	store.loadSettings()
	if got := store.GetSettings().AITagSuggestions; got == nil || *got {
		t.Errorf("toggle not persisted/reloaded: %v", got)
	}
}
