package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path"
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
	Body    string   `json:"body"`
	file    string   // path relative to the notes dir, slash-separated, no ".md"; tracks folder+date+title, server-only
	extraFM []string // frontmatter Portanote doesn't own, kept verbatim (see knownFM)
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
	tasks    []*Task // standalone to-do items (independent of notes)
	tasksMu  sync.Mutex
}

type FolderInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func NewStore(dir string) (*Store, error) {
	s := &Store{dir: dir, notes: map[string]*Note{}, idx: NewIndex()}
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	s.scanLocked()
	s.migrateLegacyFolders()
	s.migrateLegacyNotes()
	s.seedTemplates()
	s.loadSettings()
	s.loadTasks()
	return s, nil
}

// scanLocked rebuilds notes, search index, and folder list from the directory
// tree. Caller holds s.mu (or is still single-threaded in NewStore).
func (s *Store) scanLocked() {
	s.notes = map[string]*Note{}
	s.idx = NewIndex()
	s.folders = nil
	filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == s.dir {
			return nil
		}
		rel, err := filepath.Rel(s.dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// app-owned top-level dirs and dot-dirs are not note folders
			if strings.HasPrefix(d.Name(), ".") ||
				(!strings.Contains(rel, "/") && reservedFolders[strings.ToLower(d.Name())]) {
				return filepath.SkipDir
			}
			s.addFolderPathLocked(rel) // real directories (even empty) are the folder tree
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}
		if base := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())); !idRe.MatchString(base) {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		n := parseNote(strings.TrimSuffix(rel, filepath.Ext(rel)), string(raw))
		// the directory a file sits in IS its folder; a frontmatter `folder:`
		// only means something for legacy root-level files (migrated below)
		if folder := path.Dir(n.file); folder != "." {
			n.Folder = folder
		}
		// files dropped in by hand have no frontmatter timestamps — use the file's
		if n.Created.IsZero() || n.Updated.IsZero() {
			if info, err := d.Info(); err == nil {
				if n.Created.IsZero() {
					n.Created = info.ModTime().UTC()
				}
				if n.Updated.IsZero() {
					n.Updated = info.ModTime().UTC()
				}
			}
		}
		// the map key is the stable ID (from frontmatter, or the basename for
		// legacy/hand-made notes). Hand-made files in different folders can
		// share a basename — disambiguate; the suffix sticks on first save.
		if s.notes[n.ID] != nil {
			for i := 2; ; i++ {
				if cand := fmt.Sprintf("%s-%d", n.ID, i); s.notes[cand] == nil {
					n.ID = cand
					break
				}
			}
		}
		s.notes[n.ID] = n
		s.idx.Put(n.ID, n.Title, n.Tags, n.Body)
		return nil
	})
}

// RescanResult summarizes what a rescan found relative to the previous index.
type RescanResult struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
	Total   int `json:"total"`
}

// Rescan rebuilds the whole index from the directory tree on demand, adopting
// files and folders created, edited, or deleted outside the app (file
// explorer, git, another editor). IDs come from frontmatter, so surviving
// notes keep their identity. Legacy-layout files dropped in (e.g. restored
// from an old backup) are migrated exactly as at startup.
func (s *Store) Rescan() RescanResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.notes
	s.scanLocked()
	s.migrateLegacyFolders()
	s.migrateLegacyNotes()
	res := RescanResult{Total: len(s.notes)}
	for id, n := range s.notes {
		o, ok := old[id]
		switch {
		case !ok:
			res.Added++
		case n.Body != o.Body || n.Title != o.Title || n.Folder != o.Folder ||
			!slicesEqual(n.Tags, o.Tags) || n.Starred != o.Starred || n.Trashed != o.Trashed:
			res.Changed++
		}
	}
	for id := range old {
		if _, ok := s.notes[id]; !ok {
			res.Removed++
		}
	}
	return res
}

// migrateLegacyFolders converts the old .portanote-folders.json manifest into
// real directories, then removes it — directories are the manifest now.
func (s *Store) migrateLegacyFolders() {
	p := filepath.Join(s.dir, ".portanote-folders.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var m struct {
		Folders []string `json:"folders"`
	}
	if json.Unmarshal(raw, &m) == nil {
		for _, f := range dedupeFolders(m.Folders) {
			if validateFolder(f) != nil {
				log.Printf("legacy folder %q: reserved name, skipped", f)
				continue
			}
			s.ensureFolderLocked(f)
		}
	}
	os.Remove(p)
}

// migrateLegacyNotes moves root-level notes that still carry a frontmatter
// `folder:` (the old flat layout) into that folder's directory. The rewrite
// also drops the folder field — the file's location carries it from here on.
func (s *Store) migrateLegacyNotes() {
	for _, n := range s.notes {
		if n.Folder == "" || strings.Contains(n.file, "/") {
			continue
		}
		f := cleanFolderPath(n.Folder)
		if f == "" || validateFolder(f) != nil {
			log.Printf("note %q: legacy folder %q is a reserved name — leaving uncategorized", n.Title, n.Folder)
			n.Folder = ""
			continue
		}
		f = s.canonicalFolderLocked(f)
		if s.ensureFolderLocked(f) != nil {
			n.Folder = ""
			continue
		}
		oldFile, oldFolder := n.file, n.Folder
		n.Folder = f
		n.file = s.uniqueFileLocked(f, oldFile, n.ID)
		if err := s.write(n); err != nil {
			n.file, n.Folder = oldFile, oldFolder
			continue
		}
		os.Remove(s.notePath(oldFile))
	}
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
	n.file = s.uniqueFileLocked("", noteFilename(now, title), id)
	if err := s.write(n); err != nil {
		return nil, err
	}
	s.notes[id] = n
	s.idx.Put(id, n.Title, n.Tags, n.Body)
	cp := *n
	return &cp, nil
}

// uniqueFileLocked returns the dir-relative path (no extension) for a note
// named base inside folder, suffixing -2/-3/… while another note in the same
// folder claims that basename (case-insensitively — Windows filesystems are).
// Caller holds s.mu.
func (s *Store) uniqueFileLocked(folder, base, selfID string) string {
	inUse := map[string]bool{}
	for id, n := range s.notes {
		if id != selfID && strings.EqualFold(n.Folder, folder) {
			inUse[strings.ToLower(path.Base(n.file))] = true
		}
	}
	name := base
	for i := 2; inUse[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return relNotePath(folder, name)
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
	// validate the folder before mutating anything, so a bad name can't half-apply
	newFolder := n.Folder
	if req.Folder != nil {
		f := cleanFolderPath(*req.Folder)
		if err := validateFolder(f); err != nil {
			return nil, err
		}
		newFolder = s.canonicalFolderLocked(f)
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
	if newFolder != n.Folder {
		n.Folder = newFolder
		if newFolder != "" {
			s.addFolderPathLocked(newFolder) // write() below creates the directory
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
	// keep the on-disk location in sync with the folder and title (date stays as created)
	oldFile := n.file
	n.file = s.uniqueFileLocked(n.Folder, noteFilename(n.Created, n.Title), n.ID)
	if err := s.write(n); err != nil {
		return nil, err
	}
	// EqualFold: on a case-insensitive filesystem a case-only "move" rewrites
	// the same physical file — removing the old path would delete the new one
	if oldFile != "" && !strings.EqualFold(oldFile, n.file) {
		os.Remove(s.notePath(oldFile))
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
	if err := os.Remove(s.notePath(n.file)); err != nil && !os.IsNotExist(err) {
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

// notePath maps a note's dir-relative slash path (no extension) to disk.
func (s *Store) notePath(rel string) string {
	return filepath.Join(s.dir, filepath.FromSlash(rel)+".md")
}

// relNotePath joins a folder path and a file basename into the note's
// dir-relative slash path (no extension).
func relNotePath(folder, base string) string {
	if folder == "" {
		return base
	}
	return folder + "/" + base
}

func (s *Store) write(n *Note) error {
	if n.file == "" {
		n.file = relNotePath(n.Folder, noteFilename(n.Created, n.Title))
	}
	dst := s.notePath(n.file)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, []byte(serializeNote(n)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func serializeNote(n *Note) string {
	var b strings.Builder
	// no folder field: the note's directory IS its folder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %q\n", n.ID)
	fmt.Fprintf(&b, "title: %q\n", n.Title)
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
	for _, line := range n.extraFM { // whatever another tool put here, back as it was
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(n.Body)
	return b.String()
}

func parseNote(relPath, raw string) *Note {
	// ID defaults to the file's basename; frontmatter `id:` overrides it if present.
	n := &Note{Meta: Meta{ID: path.Base(relPath), Tags: []string{}}, file: relPath}
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
		n.Title = deriveTitle(path.Base(relPath), body)
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

// knownFM are the frontmatter keys Portanote owns — parsed into Meta here and
// rewritten from it by serializeNote. Every other key belongs to whatever else
// touches these files (an Obsidian property, a Hugo draft flag, OKF's type:, a
// script's own field) and is preserved verbatim: notes are just files, so an
// editor that quietly ate metadata it didn't recognize would be lying about it.
var knownFM = map[string]bool{
	"id": true, "title": true, "tags": true, "starred": true,
	"trashed": true, "created": true, "updated": true,
	"folder": true, // legacy: read to migrate to the directory layout, never written back
}

// fmContinues reports whether a frontmatter line belongs to the key above it
// (an indented mapping entry or list item) rather than starting a new key.
func fmContinues(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t')
}

func parseFrontmatter(fm string, n *Note) {
	keeping := false // inside an unowned key: capture it and whatever indents under it
	inTags := false  // inside a block-form `tags:` list, one "- tag" per line
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || fmContinues(line) {
			switch {
			case keeping:
				n.extraFM = append(n.extraFM, line)
			case inTags:
				if item, ok := strings.CutPrefix(strings.TrimSpace(line), "-"); ok {
					if t := strings.TrimSpace(unquote(strings.TrimSpace(item))); t != "" {
						n.Tags = append(n.Tags, t)
					}
				}
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		if !ok || !knownFM[key] {
			keeping, inTags = true, false // an unowned key, a comment, or a stray line
			n.extraFM = append(n.extraFM, line)
			continue
		}
		keeping, inTags = false, false
		val = strings.TrimSpace(val)
		switch key {
		case "id":
			if v := unquote(val); v != "" {
				n.ID = v
			}
		case "title":
			n.Title = unquote(val)
		case "folder": // legacy (pre-directory layout); read only to migrate
			n.Folder = unquote(val)
		case "tags":
			if val == "" { // block form (what Obsidian writes): items follow, indented
				inTags = true
				break
			}
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
	// blank lines trailing the last unowned key are padding, not content
	for len(n.extraFM) > 0 && strings.TrimSpace(n.extraFM[len(n.extraFM)-1]) == "" {
		n.extraFM = n.extraFM[:len(n.extraFM)-1]
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
// Folders form a tree: a folder is a "/"-separated path like "Work/Projects/Alpha",
// and each one is a real subdirectory of the notes dir — the directory tree IS
// the folder tree, so any file manager or editor sees the same structure.
// s.folders is just the in-memory index of it (kept so empty folders render
// without rescanning).

// reservedFolders are the app-owned top-level directories inside the notes
// dir — the scanner skips them, so notes can't live there.
var reservedFolders = map[string]bool{"templates": true, "backups": true, "attachments": true}

// Windows rejects these as file or directory names, whatever the extension.
var reservedDeviceRe = regexp.MustCompile(`^(?i:con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

var ErrInvalidFolder = errors.New("invalid folder name")

// validateFolder rejects folder paths that can't be real directories: a first
// segment that collides with an app-owned dir, or any Windows device name.
func validateFolder(p string) error {
	if p == "" {
		return nil
	}
	segs := strings.Split(p, "/")
	if reservedFolders[strings.ToLower(segs[0])] {
		return fmt.Errorf("%w: %q is reserved for Portanote itself", ErrInvalidFolder, segs[0])
	}
	for _, seg := range segs {
		if reservedDeviceRe.MatchString(seg) {
			return fmt.Errorf("%w: %q is a reserved name on Windows", ErrInvalidFolder, seg)
		}
	}
	return nil
}

// cleanFolderPath sanitizes each "/"-separated segment down to what every
// filesystem accepts (folders are real directories): control characters and
// Windows-special characters are stripped, leading/trailing dots and spaces
// trimmed, empty segments dropped — so "../x" cannot escape the notes dir.
func cleanFolderPath(path string) string {
	segs := strings.Split(path, "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		seg = strings.Map(func(r rune) rune {
			if r < 0x20 || strings.ContainsRune(`\<>:"|?*`, r) {
				return -1
			}
			return r
		}, seg)
		if seg = strings.Trim(seg, ". "); seg != "" {
			out = append(out, seg)
		}
	}
	return strings.Join(out, "/")
}

// canonicalFolderLocked snaps each path segment to the casing of an existing
// folder (Windows treats "work" and "Work" as one directory — so do we).
// Caller holds s.mu (or is still single-threaded in NewStore).
func (s *Store) canonicalFolderLocked(p string) string {
	if p == "" {
		return ""
	}
	cur := ""
	for i, seg := range strings.Split(p, "/") {
		if i == 0 {
			cur = seg
		} else {
			cur += "/" + seg
		}
		for _, f := range s.folders {
			if strings.EqualFold(f, cur) {
				cur = f
				break
			}
		}
	}
	return cur
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
	if err := validateFolder(path); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path = s.canonicalFolderLocked(path)
	if err := s.ensureFolderLocked(path); err != nil {
		return "", err
	}
	return path, nil
}

// RenameFolder renames a folder's directory — note files move with it, so the
// whole subtree re-homes with a single rename on disk. Passing a `to` with a
// different parent moves the subtree.
func (s *Store) RenameFolder(from, to string) error {
	from = cleanFolderPath(from)
	to = cleanFolderPath(to)
	if from == "" || to == "" {
		return errors.New("folder name required")
	}
	if err := validateFolder(to); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from = s.canonicalFolderLocked(from)
	// snap the destination's parent to existing casing; the leaf stays as
	// typed so a case-only rename ("work" -> "Work") still goes through
	if i := strings.LastIndex(to, "/"); i >= 0 {
		to = s.canonicalFolderLocked(to[:i]) + "/" + to[i+1:]
	}
	if from == to {
		return nil
	}
	found := false
	for _, f := range s.folders {
		if f == from {
			found = true
			break
		}
	}
	if !found {
		return errors.New("folder not found")
	}
	// a case-only rename targets the same directory; anything else must not
	// land on an existing one (directories don't merge)
	if !strings.EqualFold(from, to) {
		if underFolder(to, from) {
			return errors.New("cannot move a folder inside itself")
		}
		if _, err := os.Stat(filepath.Join(s.dir, filepath.FromSlash(to))); err == nil {
			return fmt.Errorf("a folder named %q already exists", to)
		}
	}
	dst := filepath.Join(s.dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(s.dir, filepath.FromSlash(from)), dst); err != nil {
		return err
	}
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
	// the files moved with their directory — only in-memory paths need fixing
	for _, n := range s.notes {
		if np, ok := remap(n.Folder); ok {
			n.Folder = np
			n.file = relNotePath(np, path.Base(n.file))
		}
	}
	return nil
}

// DeleteFolder removes a folder and its whole subtree; notes anywhere in that
// subtree move back to the notes root (they are not deleted). Directories are
// then removed deepest-first, and one still holding files Portanote doesn't
// know about is left on disk (it reappears as a folder on the next start)
// rather than deleting anything the app doesn't own.
func (s *Store) DeleteFolder(folder string) error {
	folder = cleanFolderPath(folder)
	if folder == "" {
		return errors.New("folder name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	folder = s.canonicalFolderLocked(folder)
	for _, n := range s.notes {
		if n.Folder == "" || !underFolder(n.Folder, folder) {
			continue
		}
		oldFile, oldFolder := n.file, n.Folder
		n.Folder = ""
		n.file = s.uniqueFileLocked("", path.Base(oldFile), n.ID)
		if err := os.Rename(s.notePath(oldFile), s.notePath(n.file)); err != nil {
			n.file, n.Folder = oldFile, oldFolder
			return err
		}
	}
	kept := s.folders[:0:0]
	for _, f := range s.folders {
		if !underFolder(f, folder) {
			kept = append(kept, f)
		}
	}
	s.folders = kept
	removeEmptyDirs(filepath.Join(s.dir, filepath.FromSlash(folder)))
	return nil
}

// removeEmptyDirs deletes root and any directories under it that are empty,
// deepest first; os.Remove refuses non-empty directories, which is exactly
// the safety net wanted here.
func removeEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Remove(dirs[i])
	}
}

// ensureFolderLocked makes the folder's directory (and ancestors) exist on
// disk and registers it in the in-memory list. Caller holds s.mu.
func (s *Store) ensureFolderLocked(folder string) error {
	if folder == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(s.dir, filepath.FromSlash(folder)), 0o755); err != nil {
		return err
	}
	s.addFolderPathLocked(folder)
	return nil
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
