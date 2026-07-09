package main

// claude_config.go — user-configurable claude executable / settings file, and
// an activity log of Ask Claude turns. Both persist in the notes dir like the
// other .portanote-*.json sidecars. The config stores only explicit overrides
// (empty = auto-detect); resolution prefers an existing override, else the
// value detected at launch, so a config synced from another machine (stale
// paths) self-heals instead of breaking the panel.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type claudeConfigData struct {
	Exe          string   `json:"exe"`            // override for the claude binary; "" = auto-detect
	SettingsFile string   `json:"settingsFile"`   // custom --settings file; "" = claude's default
	Env          []string `json:"env,omitempty"`  // extra KEY=VALUE vars for the spawned claude
}

var claudeCfg struct {
	mu   sync.RWMutex
	data claudeConfigData
	path string
}

func loadClaudeConfig(dir string) {
	claudeCfg.mu.Lock()
	claudeCfg.path = filepath.Join(dir, ".portanote-claude.json")
	claudeCfg.data = claudeConfigData{}
	if raw, err := os.ReadFile(claudeCfg.path); err == nil {
		json.Unmarshal(raw, &claudeCfg.data)
	}
	claudeCfg.mu.Unlock()
	loadClaudeLog(dir)
}

// caller holds claudeCfg.mu
func saveClaudeConfigLocked() {
	if claudeCfg.path == "" {
		return
	}
	raw, _ := json.MarshalIndent(claudeCfg.data, "", "  ")
	tmp := claudeCfg.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, claudeCfg.path)
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// resolveClaudeExe is the binary the chat spawns: the override if it exists,
// else whatever was auto-detected.
func resolveClaudeExe() string {
	claudeCfg.mu.RLock()
	override := claudeCfg.data.Exe
	claudeCfg.mu.RUnlock()
	if override != "" && fileExists(override) {
		return override
	}
	return detectedClaudeExe()
}

// detectedClaudeSettings is claude's own default user settings file, if present.
func detectedClaudeSettings() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if p := filepath.Join(home, ".claude", "settings.json"); fileExists(p) {
		return p
	}
	return ""
}

// claudeSettingsArg is the value for --settings, or "" to pass no flag. Only an
// explicit, existing override is passed; with no override claude uses its own
// default settings chain (which already includes the detected file), so the
// default turn behavior is unchanged.
func claudeSettingsArg() string {
	claudeCfg.mu.RLock()
	override := claudeCfg.data.SettingsFile
	claudeCfg.mu.RUnlock()
	if override != "" && fileExists(override) {
		return override
	}
	return ""
}

// effectiveClaudeSettings is the settings file claude actually reads (for
// display): the explicit override if it exists, else the detected default.
func effectiveClaudeSettings() string {
	if s := claudeSettingsArg(); s != "" {
		return s
	}
	return detectedClaudeSettings()
}

// sanitizeClaudeEnv keeps only well-formed KEY=VALUE entries (trimmed).
func sanitizeClaudeEnv(in []string) []string {
	out := []string{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if k, _, ok := strings.Cut(e, "="); ok && strings.TrimSpace(k) != "" {
			out = append(out, e)
		}
	}
	return out
}

// settingsFileEnv reads the "env" block from the effective settings.json and
// returns it as sorted KEY=VALUE. Claude applies that block to its own runtime,
// but too late for vars Node reads at startup (NODE_EXTRA_CA_CERTS) — hoisting
// them into the spawned process's environment here makes them take effect. Nil
// on any problem (no file, unreadable, no env block).
func settingsFileEnv() []string {
	path := effectiveClaudeSettings()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s struct {
		Env map[string]string `json:"env"`
	}
	if json.Unmarshal(raw, &s) != nil || len(s.Env) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Env))
	for k, v := range s.Env {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out
}

// claudeSpawnEnv is the environment for the spawned claude: the inherited
// environment, then the settings.json "env" block, then the manually
// configured KEY=VALUE box — later entries win, so the box overrides
// settings.json which overrides inherited. Returns nil when nothing extra is
// configured, so the child simply inherits as before.
func claudeSpawnEnv() []string {
	claudeCfg.mu.RLock()
	box := sanitizeClaudeEnv(claudeCfg.data.Env)
	claudeCfg.mu.RUnlock()
	extra := append(settingsFileEnv(), box...)
	if len(extra) == 0 {
		return nil
	}
	return append(os.Environ(), extra...)
}

type claudeConfigResp struct {
	Exe               string   `json:"exe"`
	SettingsFile      string   `json:"settingsFile"`
	Env               []string `json:"env"`
	DetectedExe       string   `json:"detectedExe"`
	DetectedSettings  string   `json:"detectedSettings"`
	EffectiveExe      string   `json:"effectiveExe"`
	EffectiveSettings string   `json:"effectiveSettings"`
	SettingsEnv       []string `json:"settingsEnv"` // env block auto-loaded from settings.json
	Available         bool     `json:"available"`
	ExeWarning        string   `json:"exeWarning,omitempty"`
	SettingsWarning   string   `json:"settingsWarning,omitempty"`
}

func claudeConfigResponse() claudeConfigResp {
	claudeCfg.mu.RLock()
	exe, settings, env := claudeCfg.data.Exe, claudeCfg.data.SettingsFile, claudeCfg.data.Env
	claudeCfg.mu.RUnlock()
	resp := claudeConfigResp{
		Exe:               exe,
		SettingsFile:      settings,
		Env:               sanitizeClaudeEnv(env),
		DetectedExe:       detectedClaudeExe(),
		DetectedSettings:  detectedClaudeSettings(),
		EffectiveExe:      resolveClaudeExe(),
		EffectiveSettings: effectiveClaudeSettings(),
		SettingsEnv:       settingsFileEnv(),
	}
	resp.Available = resp.EffectiveExe != ""
	if exe != "" && !fileExists(exe) {
		resp.ExeWarning = "that path doesn't exist — falling back to the auto-detected claude"
	}
	if settings != "" && !fileExists(settings) {
		resp.SettingsWarning = "that settings file doesn't exist — using claude's default"
	}
	return resp
}

// claudeConfigHandler serves GET (current config) and PUT (update). Leaving a
// field blank, or equal to the auto-detected value, stores it as "auto".
func claudeConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var in claudeConfigData
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		exe := strings.TrimSpace(in.Exe)
		if exe == detectedClaudeExe() { // the pre-filled default → keep tracking detection
			exe = ""
		}
		settings := strings.TrimSpace(in.SettingsFile)
		if settings == detectedClaudeSettings() {
			settings = ""
		}
		claudeCfg.mu.Lock()
		claudeCfg.data.Exe, claudeCfg.data.SettingsFile = exe, settings
		claudeCfg.data.Env = sanitizeClaudeEnv(in.Env)
		saveClaudeConfigLocked()
		claudeCfg.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, claudeConfigResponse())
}

// ---------------------------------------------------------------- activity log

type claudeLogEntry struct {
	Time      time.Time `json:"time"`
	NoteID    string    `json:"noteId,omitempty"`
	NoteTitle string    `json:"noteTitle,omitempty"`
	Prompt    string    `json:"prompt"`
	Lines     string    `json:"lines,omitempty"` // highlighted range, e.g. "2-3"
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
	Ms        int64     `json:"ms"`
}

const claudeLogMax = 200

var claudeLog struct {
	mu      sync.Mutex
	entries []claudeLogEntry
	path    string
}

func loadClaudeLog(dir string) {
	claudeLog.mu.Lock()
	defer claudeLog.mu.Unlock()
	claudeLog.path = filepath.Join(dir, ".portanote-claude-log.json")
	claudeLog.entries = nil
	if raw, err := os.ReadFile(claudeLog.path); err == nil {
		var m struct {
			Entries []claudeLogEntry `json:"entries"`
		}
		if json.Unmarshal(raw, &m) == nil {
			claudeLog.entries = m.Entries
		}
	}
}

func appendClaudeLog(e claudeLogEntry) {
	claudeLog.mu.Lock()
	defer claudeLog.mu.Unlock()
	claudeLog.entries = append(claudeLog.entries, e)
	if len(claudeLog.entries) > claudeLogMax {
		claudeLog.entries = claudeLog.entries[len(claudeLog.entries)-claudeLogMax:]
	}
	saveClaudeLogLocked()
}

// caller holds claudeLog.mu
func saveClaudeLogLocked() {
	if claudeLog.path == "" {
		return
	}
	raw, _ := json.MarshalIndent(map[string][]claudeLogEntry{"entries": claudeLog.entries}, "", "  ")
	tmp := claudeLog.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, claudeLog.path)
	}
}

func claudeLogsHandler(w http.ResponseWriter, r *http.Request) {
	claudeLog.mu.Lock()
	out := make([]claudeLogEntry, len(claudeLog.entries))
	for i, e := range claudeLog.entries { // newest first
		out[len(claudeLog.entries)-1-i] = e
	}
	claudeLog.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func claudeLogsClearHandler(w http.ResponseWriter, r *http.Request) {
	claudeLog.mu.Lock()
	claudeLog.entries = nil
	saveClaudeLogLocked()
	claudeLog.mu.Unlock()
	writeJSON(w, http.StatusOK, []claudeLogEntry{})
}

// logClaudeTurn records one completed chat turn (prompt + outcome).
func logClaudeTurn(note *Note, sel *claudeSelection, prompt string, since time.Time, err error) {
	e := claudeLogEntry{
		Time:   time.Now(),
		Prompt: prompt,
		Ms:     time.Since(since).Milliseconds(),
		OK:     err == nil,
	}
	if note != nil {
		e.NoteID, e.NoteTitle = note.ID, note.Title
	}
	if sel.valid() {
		e.Lines = fmt.Sprintf("%d-%d", sel.StartLine, sel.EndLine)
	}
	if err != nil {
		e.Error = err.Error()
	}
	appendClaudeLog(e)
}
