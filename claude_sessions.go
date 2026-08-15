package main

// claude_sessions.go — folder-scoped conversation continuity for the chat
// drawer. Each turn still spawns its own `claude -p`, so "holding a session"
// means remembering the id the CLI reports and passing --resume on the next
// turn: context rolls message to message with no long-lived child process.
//
// Scope is the note's folder, never the note. Notes in the same folder share
// one conversation and switching between them changes nothing; moving to a
// note in a different folder switches to that folder's conversation. Sessions
// never cross folders.
//
// Ids live in a .portanote-claude-sessions.json sidecar (same convention as
// the other .portanote-*.json files) rather than in note frontmatter: a
// session is machine-local scratch, and frontmatter syncs. Keeping them past
// the folder switch is what lets a later visit pick the conversation back up.
//
// Any stored id can vanish underneath us — the CLI prunes its own transcripts
// after cleanupPeriodDays (30 by default). A resume that fails with "No
// conversation found" is not an error; the turn restarts fresh and the new id
// replaces the dead one.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var claudeSessions struct {
	mu       sync.Mutex
	path     string
	byFolder map[string]string
}

func loadClaudeSessions(dir string) {
	claudeSessions.mu.Lock()
	defer claudeSessions.mu.Unlock()
	claudeSessions.path = filepath.Join(dir, ".portanote-claude-sessions.json")
	claudeSessions.byFolder = map[string]string{}
	if raw, err := os.ReadFile(claudeSessions.path); err == nil {
		var data struct {
			Folders map[string]string `json:"folders"`
		}
		if json.Unmarshal(raw, &data) == nil && data.Folders != nil {
			claudeSessions.byFolder = data.Folders
		}
	}
}

// caller holds claudeSessions.mu
func saveClaudeSessionsLocked() {
	if claudeSessions.path == "" {
		return
	}
	raw, _ := json.MarshalIndent(map[string]map[string]string{"folders": claudeSessions.byFolder}, "", "  ")
	tmp := claudeSessions.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, claudeSessions.path)
	}
}

// claudeSessionFor returns the id to resume for a folder, or "" to start
// fresh. The root folder is "", a real key — a missing entry, not an empty
// name, is what means "no conversation yet".
func claudeSessionFor(folder string) string {
	claudeSessions.mu.Lock()
	defer claudeSessions.mu.Unlock()
	return claudeSessions.byFolder[folder]
}

// claudeSessionRecord stores the id the CLI reported for a folder. Resuming
// hands back the same id it was given, so after the first turn in a folder
// this is a no-op write.
func claudeSessionRecord(folder, id string) {
	if id == "" {
		return
	}
	claudeSessions.mu.Lock()
	defer claudeSessions.mu.Unlock()
	if claudeSessions.byFolder == nil {
		claudeSessions.byFolder = map[string]string{}
	}
	if claudeSessions.byFolder[folder] == id {
		return
	}
	claudeSessions.byFolder[folder] = id
	saveClaudeSessionsLocked()
}

// claudeSessionDrop forgets a folder's conversation: the CLI pruned it, or the
// user cleared the thread and asked for a clean slate.
func claudeSessionDrop(folder string) {
	claudeSessions.mu.Lock()
	defer claudeSessions.mu.Unlock()
	if _, ok := claudeSessions.byFolder[folder]; !ok {
		return
	}
	delete(claudeSessions.byFolder, folder)
	saveClaudeSessionsLocked()
}
