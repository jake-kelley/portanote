package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Per-folder Git sync (manual): a synced folder is exported to a working clone
// under <notes>/.git-sync/<slug> and pushed/pulled on demand. Auth is a GitLab
// access token used as HTTPS Basic auth via -c http.extraHeader (kept out of the
// remote URL and reflog). There is no background auto-sync — Pull and Push only.

type SyncState struct {
	Username string                `json:"username"` // GitLab user (or "oauth2")
	Token    string                `json:"token"`
	Folders  map[string]FolderSync `json:"folders"` // folder path -> config
}

type FolderSync struct {
	RemoteURL string `json:"remoteUrl"`
	Branch    string `json:"branch"`
}

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func (s *Store) syncPath() string  { return filepath.Join(s.dir, ".portanote-sync.json") }
func (s *Store) syncRoot() string  { return filepath.Join(s.dir, ".git-sync") }
func (s *Store) cloneDir(p string) string {
	return filepath.Join(s.syncRoot(), slugify(strings.ReplaceAll(p, "/", "-")))
}

func (s *Store) loadSync() {
	s.sync = SyncState{Folders: map[string]FolderSync{}}
	if raw, err := os.ReadFile(s.syncPath()); err == nil {
		var st SyncState
		if json.Unmarshal(raw, &st) == nil {
			if st.Folders == nil {
				st.Folders = map[string]FolderSync{}
			}
			s.sync = st
		}
	}
}

func (s *Store) saveSyncLocked() {
	raw, _ := json.MarshalIndent(s.sync, "", "  ")
	tmp := s.syncPath() + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		os.Rename(tmp, s.syncPath())
	}
}

// ---------------------------------------------------------------- git plumbing

// git runs a git command in dir. When net is true, the HTTPS auth header is
// injected so pushes/pulls/ls-remote authenticate without touching the URL.
func (s *Store) git(dir string, net bool, args ...string) (string, error) {
	full := []string{}
	if net {
		s.syncMu.Lock()
		user, token := s.sync.Username, s.sync.Token
		s.syncMu.Unlock()
		if user == "" {
			user = "oauth2"
		}
		cred := base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
		full = append(full, "-c", "http.extraHeader=Authorization: Basic "+cred)
	}
	full = append(full, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (s *Store) folderSync(path string) (FolderSync, bool) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	fs, ok := s.sync.Folders[path]
	return fs, ok
}

// ensureClone makes sure a working clone exists for the folder; clones the
// remote (or inits an empty repo when the remote has no commits yet).
func (s *Store) ensureClone(path string, fs FolderSync) (string, error) {
	dir := s.cloneDir(path)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		s.git(dir, false, "remote", "set-url", "origin", fs.RemoteURL)
		return dir, nil
	}
	if err := os.MkdirAll(s.syncRoot(), 0o755); err != nil {
		return "", err
	}
	os.RemoveAll(dir)
	if out, err := s.git(s.syncRoot(), true, "clone", fs.RemoteURL, filepath.Base(dir)); err != nil {
		// empty remote (nothing to clone) -> init a fresh repo pointed at it
		if !strings.Contains(out, "empty repository") && !strings.Contains(strings.ToLower(out), "warning: you appear to have cloned an empty") {
			os.MkdirAll(dir, 0o755)
			if _, e := s.git(dir, false, "init"); e != nil {
				return "", fmt.Errorf("clone failed: %s", out)
			}
			s.git(dir, false, "remote", "add", "origin", fs.RemoteURL)
		}
	}
	s.git(dir, false, "config", "user.name", "Portanote")
	s.git(dir, false, "config", "user.email", "portanote@localhost")
	return dir, nil
}

// ---------------------------------------------------------------- operations

func (s *Store) SetGitAuth(username, token string) {
	s.syncMu.Lock()
	s.sync.Username = strings.TrimSpace(username)
	if token != "" { // empty token means "keep the stored one"
		s.sync.Token = token
	}
	s.saveSyncLocked()
	s.syncMu.Unlock()
}

func (s *Store) ConfigureFolderSync(path, remoteURL, branch string) error {
	path = cleanFolderPath(path)
	remoteURL = strings.TrimSpace(remoteURL)
	if path == "" || remoteURL == "" {
		return errors.New("folder and remote URL are required")
	}
	if branch == "" {
		branch = "main"
	}
	s.syncMu.Lock()
	s.sync.Folders[path] = FolderSync{RemoteURL: remoteURL, Branch: branch}
	s.saveSyncLocked()
	s.syncMu.Unlock()
	s.mu.Lock()
	s.addFolderPathLocked(path)
	s.saveFolders()
	s.mu.Unlock()
	return nil
}

func (s *Store) UnlinkFolderSync(path string) {
	s.syncMu.Lock()
	delete(s.sync.Folders, path)
	s.saveSyncLocked()
	s.syncMu.Unlock()
	os.RemoveAll(s.cloneDir(path))
}

func (s *Store) SetSyncBranch(path, branch string) {
	s.syncMu.Lock()
	if fs, ok := s.sync.Folders[path]; ok {
		fs.Branch = strings.TrimSpace(branch)
		s.sync.Folders[path] = fs
		s.saveSyncLocked()
	}
	s.syncMu.Unlock()
}

func (s *Store) SyncBranches(path string) ([]string, error) {
	fs, ok := s.folderSync(path)
	if !ok {
		return nil, errors.New("folder not configured for sync")
	}
	out, err := s.git(s.dir, true, "ls-remote", "--heads", fs.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("could not list branches: %s", strings.TrimSpace(out))
	}
	var branches []string
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "refs/heads/"); i >= 0 {
			branches = append(branches, strings.TrimSpace(line[i+len("refs/heads/"):]))
		}
	}
	return branches, nil
}

// SyncPull fetches the folder's branch and imports incoming notes into the store.
func (s *Store) SyncPull(path string) (string, error) {
	fs, ok := s.folderSync(path)
	if !ok {
		return "", errors.New("folder not configured for sync")
	}
	dir, err := s.ensureClone(path, fs)
	if err != nil {
		return "", err
	}
	var log strings.Builder
	step := func(net bool, args ...string) error {
		out, e := s.git(dir, net, args...)
		fmt.Fprintf(&log, "$ git %s\n%s\n", strings.Join(args, " "), strings.TrimSpace(out))
		return e
	}
	if err := step(true, "fetch", "origin"); err != nil {
		return log.String(), errors.New("git fetch failed")
	}
	// check out the branch (create tracking branch from origin/<branch> if needed)
	if err := step(false, "checkout", "-B", fs.Branch, "--track", "origin/"+fs.Branch); err != nil {
		// remote branch may not exist yet — just be on the branch locally
		step(false, "checkout", "-B", fs.Branch)
	}
	if err := step(true, "merge", "--no-edit", "origin/"+fs.Branch); err != nil {
		fmt.Fprint(&log, "(nothing to merge or merge conflict — resolve in .git-sync if conflicted)\n")
	}
	count, _ := s.importFolder(path, dir)
	fmt.Fprintf(&log, "imported %d note(s) from %s\n", count, fs.Branch)
	return log.String(), nil
}

// SyncPush exports the folder's notes, commits, and pushes to the branch.
func (s *Store) SyncPush(path, message string) (string, error) {
	fs, ok := s.folderSync(path)
	if !ok {
		return "", errors.New("folder not configured for sync")
	}
	dir, err := s.ensureClone(path, fs)
	if err != nil {
		return "", err
	}
	if message == "" {
		message = "Update notes from Portanote"
	}
	var log strings.Builder
	step := func(net bool, args ...string) (string, error) {
		out, e := s.git(dir, net, args...)
		fmt.Fprintf(&log, "$ git %s\n%s\n", strings.Join(args, " "), strings.TrimSpace(out))
		return out, e
	}
	step(false, "checkout", "-B", fs.Branch)
	n := s.exportFolder(path, dir)
	fmt.Fprintf(&log, "exported %d note(s)\n", n)
	step(false, "add", "-A")
	out, _ := step(false, "-c", "user.name=Portanote", "-c", "user.email=portanote@localhost", "commit", "-m", message)
	if strings.Contains(out, "nothing to commit") {
		fmt.Fprint(&log, "(no local changes to commit)\n")
	}
	if _, err := step(true, "push", "origin", fs.Branch); err != nil {
		return log.String(), errors.New("git push failed — check token, URL, and branch permissions")
	}
	return log.String(), nil
}

// ---------------------------------------------------------------- import/export

// exportFolder writes every note in the folder subtree to the clone and removes
// clone files that no longer have a matching note (so local deletions propagate).
func (s *Store) exportFolder(path, dir string) int {
	s.mu.RLock()
	want := map[string]string{} // basename -> serialized
	for _, n := range s.notes {
		if n.Trashed || !underFolder(n.Folder, path) {
			continue
		}
		cp := *n
		want[cp.file] = serializeNote(&cp)
	}
	s.mu.RUnlock()
	for base, content := range want {
		os.WriteFile(filepath.Join(dir, base+".md"), []byte(content), 0o644)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		if _, ok := want[strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))]; !ok {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return len(want)
}

// importFolder reads .md files from the clone and upserts them into the store.
func (s *Store) importFolder(path, dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		n := parseNote(base, string(raw))
		if n.Folder == "" {
			n.Folder = path
		}
		if s.upsertFromSync(n) {
			count++
		}
	}
	return count, nil
}

// upsertFromSync creates or updates a note from a synced file, keyed by its
// stable id. Returns true if anything changed. Only content fields are synced.
func (s *Store) upsertFromSync(in *Note) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.notes[in.ID]; ok {
		if existing.Title == in.Title && existing.Body == in.Body &&
			existing.Folder == in.Folder && slicesEqual(existing.Tags, in.Tags) {
			return false
		}
		existing.Title, existing.Body, existing.Folder, existing.Tags = in.Title, in.Body, in.Folder, in.Tags
		if !in.Updated.IsZero() {
			existing.Updated = in.Updated
		}
		existing.file = s.uniqueFilenameLocked(noteFilename(existing.Created, existing.Title), existing.ID)
		s.write(existing)
		s.idx.Put(existing.ID, existing.Title, existing.Tags, existing.Body)
	} else {
		if in.Created.IsZero() {
			in.Created = time.Now().UTC()
		}
		if in.Updated.IsZero() {
			in.Updated = in.Created
		}
		in.file = s.uniqueFilenameLocked(noteFilename(in.Created, in.Title), in.ID)
		s.notes[in.ID] = in
		s.write(in)
		s.idx.Put(in.ID, in.Title, in.Tags, in.Body)
	}
	if in.Folder != "" {
		s.addFolderPathLocked(in.Folder)
		s.saveFolders()
	}
	return true
}

// ---------------------------------------------------------------- status (for API)

type SyncFolderStatus struct {
	Path      string `json:"path"`
	RemoteURL string `json:"remoteUrl"`
	Branch    string `json:"branch"`
}

type SyncStatus struct {
	GitAvailable bool               `json:"gitAvailable"`
	HasToken     bool               `json:"hasToken"`
	Username     string             `json:"username"`
	Folders      []SyncFolderStatus `json:"folders"`
}

func (s *Store) SyncStatus() SyncStatus {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	st := SyncStatus{GitAvailable: gitAvailable(), HasToken: s.sync.Token != "", Username: s.sync.Username}
	for p, fs := range s.sync.Folders {
		st.Folders = append(st.Folders, SyncFolderStatus{Path: p, RemoteURL: fs.RemoteURL, Branch: fs.Branch})
	}
	return st
}
