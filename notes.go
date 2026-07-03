package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Meta struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Folder  string    `json:"folder"`
	Tags    []string  `json:"tags"`
	Starred bool      `json:"starred"`
	Trashed bool      `json:"trashed"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

type Note struct {
	Meta
	Body string `json:"body"`
}

type ListItem struct {
	Meta
	Snippet string `json:"snippet"`
}

var ErrNotFound = errors.New("note not found")

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()-]*$`)

type Store struct {
	dir     string
	mu      sync.RWMutex
	notes   map[string]*Note
	idx     *Index
	folders []string // ordered folder names (persisted so empty folders survive)
}

type FolderInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func NewStore(dir string) (*Store, error) {
	s := &Store{dir: dir, notes: map[string]*Note{}, idx: NewIndex()}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if !idRe.MatchString(id) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		n := parseNote(id, string(raw))
		// files dropped in by hand have no frontmatter timestamps — use the file's
		if n.Created.IsZero() || n.Updated.IsZero() {
			if info, err := e.Info(); err == nil {
				if n.Created.IsZero() {
					n.Created = info.ModTime().UTC()
				}
				if n.Updated.IsZero() {
					n.Updated = info.ModTime().UTC()
				}
			}
		}
		s.notes[id] = n
		s.idx.Put(id, n.Title, n.Tags, n.Body)
	}
	s.loadFolders()
	return s, nil
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.notes)
}

func (s *Store) List() []ListItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]ListItem, 0, len(s.notes))
	for _, n := range s.notes {
		items = append(items, ListItem{Meta: n.Meta, Snippet: snippet(n.Body, 180)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Updated.After(items[j].Updated) })
	return items
}

func (s *Store) Get(id string) (*Note, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.notes[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *n
	return &cp, nil
}

func (s *Store) Create(title string) (*Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Second)
	b := make([]byte, 3)
	rand.Read(b)
	id := now.Format("20060102-150405") + "-" + hex.EncodeToString(b)
	n := &Note{Meta: Meta{ID: id, Title: title, Tags: []string{}, Created: now, Updated: now}}
	if err := s.write(n); err != nil {
		return nil, err
	}
	s.notes[id] = n
	s.idx.Put(id, n.Title, n.Tags, n.Body)
	cp := *n
	return &cp, nil
}

type UpdateReq struct {
	Title   *string   `json:"title"`
	Body    *string   `json:"body"`
	Folder  *string   `json:"folder"`
	Tags    *[]string `json:"tags"`
	Starred *bool     `json:"starred"`
	Trashed *bool     `json:"trashed"`
}

func (s *Store) Update(id string, req UpdateReq) (*Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.notes[id]
	if !ok {
		return nil, ErrNotFound
	}
	contentChanged := false
	if req.Title != nil && *req.Title != n.Title {
		n.Title = *req.Title
		contentChanged = true
	}
	if req.Body != nil && *req.Body != n.Body {
		n.Body = *req.Body
		contentChanged = true
	}
	if req.Tags != nil {
		tags := cleanTags(*req.Tags)
		if !slicesEqual(tags, n.Tags) {
			n.Tags = tags
			contentChanged = true
		}
	}
	// folder is organizational (like starred) — a move shouldn't bump the edit time
	if req.Folder != nil {
		f := cleanFolderName(*req.Folder)
		if f != n.Folder {
			n.Folder = f
			if f != "" {
				s.ensureFolderLocked(f)
			}
		}
	}
	if req.Starred != nil {
		n.Starred = *req.Starred
	}
	if req.Trashed != nil {
		n.Trashed = *req.Trashed
	}
	// starring/trashing alone shouldn't reshuffle the "recently edited" order
	if contentChanged {
		n.Updated = time.Now().UTC().Truncate(time.Second)
	}
	if err := s.write(n); err != nil {
		return nil, err
	}
	s.idx.Put(id, n.Title, n.Tags, n.Body)
	cp := *n
	return &cp, nil
}

// Delete permanently removes a note; the API layer only allows it for trashed notes.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.notes[id]; !ok {
		return ErrNotFound
	}
	if err := os.Remove(filepath.Join(s.dir, id+".md")); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.notes, id)
	s.idx.Remove(id)
	return nil
}

// ---------------------------------------------------------------- search

type SearchResult struct {
	ListItem
	Score float64 `json:"score"`
}

func (s *Store) Search(q string, includeTrashed bool) []SearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	scores := s.idx.Search(q)
	lq := strings.ToLower(strings.TrimSpace(q))
	terms := tokenize(q)

	// substring boosts catch what token search can't (partial words, symbols)
	for id, n := range s.notes {
		lt := strings.ToLower(n.Title)
		if lq != "" && strings.Contains(lt, lq) {
			scores[id] += 12
			if strings.HasPrefix(lt, lq) {
				scores[id] += 6
			}
		} else if lq != "" && strings.Contains(strings.ToLower(n.Body), lq) {
			scores[id] += 3
		}
	}

	results := make([]SearchResult, 0, len(scores))
	for id, sc := range scores {
		if sc <= 0 {
			continue
		}
		n, ok := s.notes[id]
		if !ok || (n.Trashed && !includeTrashed) {
			continue
		}
		results = append(results, SearchResult{
			ListItem: ListItem{Meta: n.Meta, Snippet: matchSnippet(n.Body, lq, terms)},
			Score:    sc,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Updated.After(results[j].Updated)
	})
	if len(results) > 200 {
		results = results[:200]
	}
	return results
}

// ---------------------------------------------------------------- files

func (s *Store) write(n *Note) error {
	path := filepath.Join(s.dir, n.ID+".md")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(serializeNote(n)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func serializeNote(n *Note) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %q\n", n.Title)
	fmt.Fprintf(&b, "folder: %q\n", n.Folder)
	b.WriteString("tags: [")
	for i, t := range n.Tags {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(t)
	}
	b.WriteString("]\n")
	fmt.Fprintf(&b, "starred: %v\n", n.Starred)
	fmt.Fprintf(&b, "trashed: %v\n", n.Trashed)
	fmt.Fprintf(&b, "created: %s\n", n.Created.Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", n.Updated.Format(time.RFC3339))
	b.WriteString("---\n\n")
	b.WriteString(n.Body)
	return b.String()
}

func parseNote(id, raw string) *Note {
	n := &Note{Meta: Meta{ID: id, Tags: []string{}}}
	body := raw
	if strings.HasPrefix(raw, "---\n") || strings.HasPrefix(raw, "---\r\n") {
		rest := raw[strings.Index(raw, "\n")+1:]
		if end := findFrontmatterEnd(rest); end >= 0 {
			parseFrontmatter(rest[:end], n)
			body = rest[end:]
			body = strings.TrimPrefix(body, "---")
			body = strings.TrimLeft(body, "\r\n")
		}
	}
	n.Body = body
	if n.Title == "" {
		n.Title = deriveTitle(id, body)
	}
	return n
}

func findFrontmatterEnd(s string) int {
	offset := 0
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.TrimRight(line, "\r\n") == "---" {
			return offset
		}
		offset += len(line)
	}
	return -1
}

func parseFrontmatter(fm string, n *Note) {
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimRight(line, "\r")
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "title":
			n.Title = unquote(val)
		case "folder":
			n.Folder = unquote(val)
		case "tags":
			val = strings.Trim(val, "[]")
			for _, t := range strings.Split(val, ",") {
				if t = strings.TrimSpace(unquote(strings.TrimSpace(t))); t != "" {
					n.Tags = append(n.Tags, t)
				}
			}
		case "starred":
			n.Starred = val == "true"
		case "trashed":
			n.Trashed = val == "true"
		case "created":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				n.Created = t
			}
		case "updated":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				n.Updated = t
			}
		}
	}
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
		return s[1 : len(s)-1]
	}
	return s
}

func deriveTitle(id, body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			if len(line) > 80 {
				line = line[:80]
			}
			return line
		}
	}
	return id
}

// ---------------------------------------------------------------- helpers

var (
	mdStrip   = regexp.MustCompile("[#*_`>~]|!?\\[([^\\]]*)\\]\\(([^)]*)\\)")
	wsCollaps = regexp.MustCompile(`\s+`)
)

func plainText(body string) string {
	t := mdStrip.ReplaceAllString(body, "$1")
	return strings.TrimSpace(wsCollaps.ReplaceAllString(t, " "))
}

func snippet(body string, max int) string {
	t := plainText(body)
	if len(t) > max {
		t = t[:max]
		if i := strings.LastIndex(t, " "); i > max/2 {
			t = t[:i]
		}
		t += "…"
	}
	return t
}

// matchSnippet returns a window of text centered on the first query match.
func matchSnippet(body, lq string, terms []string) string {
	t := plainText(body)
	lt := strings.ToLower(t)
	pos := -1
	if lq != "" {
		pos = strings.Index(lt, lq)
	}
	if pos < 0 {
		for _, term := range terms {
			if p := strings.Index(lt, term); p >= 0 && (pos < 0 || p < pos) {
				pos = p
			}
		}
	}
	if pos < 0 {
		return snippet(body, 180)
	}
	start := pos - 70
	if start < 0 {
		start = 0
	} else if i := strings.LastIndex(t[:start+20], " "); i > 0 {
		start = i + 1
	}
	end := pos + 110
	if end > len(t) {
		end = len(t)
	}
	out := t[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(t) {
		out += "…"
	}
	return out
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.Trim(strings.TrimSpace(t), ",[]\"")
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- folders

func cleanFolderName(name string) string {
	// folders are labels, not paths — no slashes or control chars
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, name)
	return strings.TrimSpace(name)
}

// Folders returns the ordered folder list with live note counts (trashed excluded).
func (s *Store) Folders() []FolderInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	counts := map[string]int{}
	for _, n := range s.notes {
		if n.Trashed || n.Folder == "" {
			continue
		}
		counts[strings.ToLower(n.Folder)]++
	}
	out := make([]FolderInfo, 0, len(s.folders))
	for _, name := range s.folders {
		out = append(out, FolderInfo{Name: name, Count: counts[strings.ToLower(name)]})
	}
	return out
}

func (s *Store) CreateFolder(name string) (string, error) {
	name = cleanFolderName(name)
	if name == "" {
		return "", errors.New("folder name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.folders {
		if strings.EqualFold(f, name) {
			return f, nil // already exists — return canonical
		}
	}
	s.folders = append(s.folders, name)
	return name, s.saveFolders()
}

func (s *Store) RenameFolder(from, to string) error {
	to = cleanFolderName(to)
	if to == "" {
		return errors.New("folder name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	renamed := false
	for i, f := range s.folders {
		if strings.EqualFold(f, from) {
			s.folders[i] = to
			renamed = true
		}
	}
	if !renamed {
		s.folders = append(s.folders, to)
	}
	s.folders = dedupeFolders(s.folders)
	for _, n := range s.notes {
		if strings.EqualFold(n.Folder, from) {
			n.Folder = to
			if err := s.write(n); err != nil {
				return err
			}
		}
	}
	return s.saveFolders()
}

// DeleteFolder removes the folder; notes inside it become uncategorized.
func (s *Store) DeleteFolder(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.folders[:0:0]
	for _, f := range s.folders {
		if !strings.EqualFold(f, name) {
			kept = append(kept, f)
		}
	}
	s.folders = kept
	for _, n := range s.notes {
		if strings.EqualFold(n.Folder, name) {
			n.Folder = ""
			if err := s.write(n); err != nil {
				return err
			}
		}
	}
	return s.saveFolders()
}

// ensureFolderLocked adds a folder to the manifest if missing. Caller holds s.mu.
func (s *Store) ensureFolderLocked(name string) {
	for _, f := range s.folders {
		if strings.EqualFold(f, name) {
			return
		}
	}
	s.folders = append(s.folders, name)
	s.saveFolders()
}

func (s *Store) foldersPath() string {
	return filepath.Join(s.dir, ".portanote-folders.json")
}

// loadFolders reads the manifest and folds in any folder a note references
// but the manifest doesn't list yet (e.g. hand-edited frontmatter).
func (s *Store) loadFolders() {
	if raw, err := os.ReadFile(s.foldersPath()); err == nil {
		var m struct {
			Folders []string `json:"folders"`
		}
		if json.Unmarshal(raw, &m) == nil {
			s.folders = dedupeFolders(m.Folders)
		}
	}
	seen := map[string]bool{}
	for _, f := range s.folders {
		seen[strings.ToLower(f)] = true
	}
	extra := map[string]string{}
	for _, n := range s.notes {
		if n.Folder != "" && !seen[strings.ToLower(n.Folder)] {
			extra[strings.ToLower(n.Folder)] = n.Folder
		}
	}
	names := make([]string, 0, len(extra))
	for _, v := range extra {
		names = append(names, v)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	s.folders = append(s.folders, names...)
}

func (s *Store) saveFolders() error {
	raw, _ := json.MarshalIndent(map[string][]string{"folders": s.folders}, "", "  ")
	tmp := s.foldersPath() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.foldersPath())
}

func dedupeFolders(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, f := range in {
		f = cleanFolderName(f)
		k := strings.ToLower(f)
		if f == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	return out
}
