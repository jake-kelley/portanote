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
	Status  string    `json:"status"` // "" | backlog | doing | done  (kanban/to-do)
	Tags    []string  `json:"tags"`
	Starred bool      `json:"starred"`
	Trashed bool      `json:"trashed"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

type Note struct {
	Meta
	Body string `json:"body"`
	file string // on-disk basename (no .md); tracks date+title, server-only
}

type ListItem struct {
	Meta
	Snippet string `json:"snippet"`
}

var ErrNotFound = errors.New("note not found")

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._()-]*$`)

// noteFilename builds the on-disk basename "DDMONTHYYYY-title-slug", e.g.
// created 2026-07-03 + title "Test Deployment" -> "03JULY2026-test-deployment".
func noteFilename(created time.Time, title string) string {
	stamp := created.Format("02") + strings.ToUpper(created.Format("January")) + created.Format("2006")
	slug := slugify(title)
	if slug == "" {
		slug = "untitled"
	}
	return stamp + "-" + slug
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case r == ' ' || r == '-' || r == '_' || r == '/' || r == '.':
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}

type Store struct {
	dir      string
	mu       sync.RWMutex
	notes    map[string]*Note
	idx      *Index
	folders  []string // ordered folder names (persisted so empty folders survive)
	settings Settings
	setMu    sync.Mutex
	sync     SyncState
	syncMu   sync.Mutex
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
		fileBase := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if !idRe.MatchString(fileBase) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		n := parseNote(fileBase, string(raw))
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
		// the map key is the stable ID (from frontmatter, or the filename for
		// legacy/hand-made notes); n.file is the actual basename on disk
		s.notes[n.ID] = n
		s.idx.Put(n.ID, n.Title, n.Tags, n.Body)
	}
	s.loadFolders()
	s.seedTemplates()
	s.loadSettings()
	s.loadSync()
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
	n.file = s.uniqueFilenameLocked(noteFilename(now, title), id)
	if err := s.write(n); err != nil {
		return nil, err
	}
	s.notes[id] = n
	s.idx.Put(id, n.Title, n.Tags, n.Body)
	cp := *n
	return &cp, nil
}

// uniqueFilenameLocked returns base, or base-2/base-3/… if another note already
// occupies that filename. Caller holds s.mu.
func (s *Store) uniqueFilenameLocked(base, selfID string) string {
	inUse := map[string]bool{}
	for id, n := range s.notes {
		if id != selfID {
			inUse[n.file] = true
		}
	}
	if !inUse[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !inUse[cand] {
			return cand
		}
	}
}

type UpdateReq struct {
	Title   *string   `json:"title"`
	Body    *string   `json:"body"`
	Folder  *string   `json:"folder"`
	Status  *string   `json:"status"`
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
		f := cleanFolderPath(*req.Folder)
		if f != n.Folder {
			n.Folder = f
			if f != "" {
				s.ensureFolderLocked(f)
			}
		}
	}
	if req.Status != nil {
		n.Status = strings.TrimSpace(*req.Status)
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
	// keep the on-disk filename in sync with the title (date stays as created)
	oldFile := n.file
	n.file = s.uniqueFilenameLocked(noteFilename(n.Created, n.Title), n.ID)
	if err := s.write(n); err != nil {
		return nil, err
	}
	if oldFile != "" && oldFile != n.file {
		os.Remove(filepath.Join(s.dir, oldFile+".md"))
	}
	s.idx.Put(id, n.Title, n.Tags, n.Body)
	cp := *n
	return &cp, nil
}

// Delete permanently removes a note; the API layer only allows it for trashed notes.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.notes[id]
	if !ok {
		return ErrNotFound
	}
	if err := os.Remove(filepath.Join(s.dir, n.file+".md")); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.notes, id)
	s.idx.Remove(id)
	return nil
}

// [[Note Title]] or [[Note Title|alias]]
var wikilinkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)

// Backlinks returns the (non-trashed) notes whose body contains a [[wiki link]]
// to the given note's title.
func (s *Store) Backlinks(id string) []ListItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, ok := s.notes[id]
	if !ok {
		return []ListItem{}
	}
	tt := strings.ToLower(strings.TrimSpace(target.Title))
	out := []ListItem{}
	if tt == "" {
		return out
	}
	for _, n := range s.notes {
		if n.ID == id || n.Trashed {
			continue
		}
		for _, m := range wikilinkRe.FindAllStringSubmatch(n.Body, -1) {
			if strings.ToLower(strings.TrimSpace(m[1])) == tt {
				out = append(out, ListItem{Meta: n.Meta, Snippet: snippet(n.Body, 120)})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
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
	if n.file == "" {
		n.file = noteFilename(n.Created, n.Title)
	}
	path := filepath.Join(s.dir, n.file+".md")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(serializeNote(n)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func serializeNote(n *Note) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %q\n", n.ID)
	fmt.Fprintf(&b, "title: %q\n", n.Title)
	fmt.Fprintf(&b, "folder: %q\n", n.Folder)
	if n.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", n.Status)
	}
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

func parseNote(fileBase, raw string) *Note {
	// ID defaults to the filename; frontmatter `id:` overrides it if present.
	n := &Note{Meta: Meta{ID: fileBase, Tags: []string{}}, file: fileBase}
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
		n.Title = deriveTitle(fileBase, body)
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
		case "id":
			if v := unquote(val); v != "" {
				n.ID = v
			}
		case "title":
			n.Title = unquote(val)
		case "folder":
			n.Folder = unquote(val)
		case "status":
			n.Status = unquote(val)
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
//
// Folders form a tree: a folder is a "/"-separated path like "Work/Projects/Alpha".
// A note's Folder field holds one such path; ancestor paths are kept in the
// manifest so intermediate (even empty) folders render in the tree.

// cleanFolderPath sanitizes each "/"-separated segment and drops empty ones.
func cleanFolderPath(path string) string {
	segs := strings.Split(path, "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		seg = strings.Map(func(r rune) rune {
			if r < 0x20 || r == '\\' {
				return -1
			}
			return r
		}, seg)
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/")
}

// ancestorPaths returns the ancestors of p, nearest-root first (excludes p).
// "a/b/c" -> ["a", "a/b"]
func ancestorPaths(p string) []string {
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs)-1)
	for i := 1; i < len(segs); i++ {
		out = append(out, strings.Join(segs[:i], "/"))
	}
	return out
}

// underFolder reports whether path is folder itself or nested beneath it.
func underFolder(path, folder string) bool {
	lp, lf := strings.ToLower(path), strings.ToLower(folder)
	return lp == lf || strings.HasPrefix(lp, lf+"/")
}

// addFolderPathLocked adds path and any missing ancestors to the manifest
// (no save). Caller holds s.mu.
func (s *Store) addFolderPathLocked(path string) {
	has := func(p string) bool {
		for _, f := range s.folders {
			if strings.EqualFold(f, p) {
				return true
			}
		}
		return false
	}
	for _, p := range append(ancestorPaths(path), path) {
		if !has(p) {
			s.folders = append(s.folders, p)
		}
	}
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

func (s *Store) CreateFolder(path string) (string, error) {
	path = cleanFolderPath(path)
	if path == "" {
		return "", errors.New("folder name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.folders {
		if strings.EqualFold(f, path) {
			return f, nil // already exists — return canonical
		}
	}
	s.addFolderPathLocked(path)
	return path, s.saveFolders()
}

// RenameFolder renames a folder and re-homes the whole subtree beneath it
// (both the manifest and every affected note). Passing a `to` with a different
// parent effectively moves the subtree.
func (s *Store) RenameFolder(from, to string) error {
	from = cleanFolderPath(from)
	to = cleanFolderPath(to)
	if from == "" || to == "" {
		return errors.New("folder name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	remap := func(p string) (string, bool) {
		if strings.EqualFold(p, from) {
			return to, true
		}
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(from)+"/") {
			return to + p[len(from):], true // keep the suffix (incl. leading "/")
		}
		return p, false
	}
	for i, f := range s.folders {
		if np, ok := remap(f); ok {
			s.folders[i] = np
		}
	}
	s.addFolderPathLocked(to)
	s.folders = dedupeFolders(s.folders)
	for _, n := range s.notes {
		if np, ok := remap(n.Folder); ok {
			n.Folder = np
			if err := s.write(n); err != nil {
				return err
			}
		}
	}
	return s.saveFolders()
}

// DeleteFolder removes a folder and its whole subtree; notes anywhere in that
// subtree become uncategorized (they are not deleted).
func (s *Store) DeleteFolder(path string) error {
	path = cleanFolderPath(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.folders[:0:0]
	for _, f := range s.folders {
		if !underFolder(f, path) {
			kept = append(kept, f)
		}
	}
	s.folders = kept
	for _, n := range s.notes {
		if n.Folder != "" && underFolder(n.Folder, path) {
			n.Folder = ""
			if err := s.write(n); err != nil {
				return err
			}
		}
	}
	return s.saveFolders()
}

// ensureFolderLocked adds a folder path (and ancestors) if missing, then saves.
// Caller holds s.mu.
func (s *Store) ensureFolderLocked(path string) {
	before := len(s.folders)
	s.addFolderPathLocked(path)
	if len(s.folders) != before {
		s.saveFolders()
	}
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
	// backfill any missing ancestors so the tree has no gaps
	for _, f := range append([]string{}, s.folders...) {
		s.addFolderPathLocked(f)
	}
	s.folders = dedupeFolders(s.folders)
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
		f = cleanFolderPath(f)
		k := strings.ToLower(f)
		if f == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	return out
}
