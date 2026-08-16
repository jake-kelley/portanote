package main

// The reader half of the .portanote bundle format (see share.go for the
// shared contract, share_export.go for the writer). This file also owns the
// foreign-markdown import path and the Store.Import method both paths land
// on.
//
// A bundle came from someone else's machine, so ReadBundle treats it as
// hostile: sizes are counted as bytes are actually copied (never trusted from
// the zip header), entry names are used only as map keys — never as a
// filesystem path — and attachment bytes are sniffed rather than trusted by
// extension.

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// readZipEntryLimited copies one entry's bytes, capping the read at
// maxEntryBytes+1 regardless of what the zip header claims — a zip bomb lies
// about its uncompressed size, so the only trustworthy count is what actually
// came out of the decompressor. total tracks the running sum across the
// whole archive so a swarm of individually-small entries can't add up past
// maxBundleBytes either.
func readZipEntryLimited(f *zip.File, total *int64) ([]byte, error) {
	if *total > maxBundleBytes {
		return nil, fmt.Errorf("bundle exceeds the %d byte total limit", maxBundleBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", f.Name, err)
	}
	if int64(len(data)) > maxEntryBytes {
		return nil, fmt.Errorf("%q exceeds the %d byte per-file limit", f.Name, maxEntryBytes)
	}
	*total += int64(len(data))
	if *total > maxBundleBytes {
		return nil, fmt.Errorf("bundle exceeds the %d byte total limit", maxBundleBytes)
	}
	return data, nil
}

// bundleEntryName reports whether name is exactly one path segment under
// prefix (e.g. "attachments/foo.png", not "attachments/x/foo.png" and not
// "attachments/../../evil.png" — that one fails the "no further slash" test
// too, on top of never being used as a filesystem path). Anything else is
// ignored rather than rejected, so a future format version can add its own
// top-level entries without breaking this reader.
func bundleEntryName(name, prefix string) (rest string, ok bool) {
	rest, ok = strings.CutPrefix(name, prefix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// sniffImageExt reports the extension for data's actual content, cross-checked
// against imgExt (api.go) — the same table the upload endpoint uses, so a
// bundle attachment is held to the same standard as a pasted screenshot. The
// declared name and extension in the archive are attacker-controlled; only
// the bytes are trustworthy.
func sniffImageExt(data []byte) (string, bool) {
	ct := http.DetectContentType(data)
	ext, ok := imgExt[ct]
	return ext, ok
}

// ReadBundle decodes a .portanote zip into the notes it carries, not yet
// written anywhere. See the package comment above for the threat model.
func ReadBundle(data []byte) ([]Incoming, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a valid .portanote file: %w", err)
	}
	if len(zr.File) > maxBundleFiles {
		return nil, fmt.Errorf("bundle has %d entries, more than the %d allowed", len(zr.File), maxBundleFiles)
	}

	var manifestFile *zip.File
	notesByPath := map[string][]byte{}
	attachByPath := map[string][]byte{}
	var total int64

	for _, f := range zr.File {
		switch {
		case f.Name == manifestPath:
			manifestFile = f
		case func() bool { _, ok := bundleEntryName(f.Name, bundleNotesDir); return ok && strings.HasSuffix(f.Name, ".md") }():
			data, err := readZipEntryLimited(f, &total)
			if err != nil {
				return nil, err
			}
			notesByPath[f.Name] = data
		case func() bool { _, ok := bundleEntryName(f.Name, bundleAttachDir); return ok }():
			data, err := readZipEntryLimited(f, &total)
			if err != nil {
				return nil, err
			}
			if _, ok := sniffImageExt(data); ok {
				attachByPath[f.Name] = data
			}
			// Not a recognized image: dropped. Any note that references it
			// picks up a "missing" warning below, same as a reference to a
			// file that was never in the zip at all.
		default:
			// Unknown top-level entry: forward compatibility for a future
			// bundle version, not an error.
		}
	}

	if manifestFile == nil {
		return nil, errors.New("bundle is missing manifest.json")
	}
	mdata, err := readZipEntryLimited(manifestFile, &total)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(mdata, &m); err != nil {
		return nil, fmt.Errorf("manifest.json is corrupt: %w", err)
	}
	if m.Format != bundleFormat {
		return nil, fmt.Errorf("not a Portanote bundle (format %q)", m.Format)
	}
	if m.V < 1 || m.V > bundleVersion {
		return nil, fmt.Errorf("bundle format v%d is not supported (this Portanote understands up to v%d)", m.V, bundleVersion)
	}

	out := make([]Incoming, 0, len(m.Notes))
	for _, mn := range m.Notes {
		raw, ok := notesByPath[mn.File]
		if !ok {
			return nil, fmt.Errorf("manifest refers to %q, not present in the bundle", mn.File)
		}
		base := strings.TrimSuffix(path.Base(mn.File), ".md")
		n := parseNote(base, string(raw))
		n.ID = "" // the store mints the real id; the sender's is never reused

		attachments := map[string][]byte{}
		var warnings []string
		for _, name := range attachRefsIn(n.Body) {
			key := bundleAttachDir + name
			if data, ok := attachByPath[key]; ok {
				attachments[key] = data
			} else {
				warnings = append(warnings, fmt.Sprintf("attachment %q is referenced but missing (or not a recognized image) in the bundle", key))
			}
		}

		out = append(out, Incoming{
			Note:        n,
			Folder:      mn.Folder,
			Attachments: attachments,
			Warnings:    warnings,
		})
	}
	return out, nil
}

// ParseMarkdownImport reads one markdown file — Portanote's own, or a plain
// export from Obsidian, Hugo, Jekyll, or nothing at all — into an Incoming.
// parseNote already handles frontmatter (or its absence) and title fallback;
// this fills in only what a foreign file can't supply itself: a fresh id and
// timestamps a plain .md doesn't carry.
func ParseMarkdownImport(name string, raw []byte, mtime time.Time) (*Incoming, error) {
	base := strings.TrimSuffix(name, ".md")
	n := parseNote(base, string(raw))
	n.ID = "" // parseNote defaults id to the basename; two READMEs would collide

	fallback := mtime
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	fallback = fallback.UTC().Truncate(time.Second)

	if n.Created.IsZero() {
		// `date:` is Hugo/Jekyll's created-at and isn't a key Portanote owns,
		// so parseNote left it sitting in extraFM untouched. Read it without
		// consuming it — the file still needs to say `date:` for the tool it
		// came from.
		if v, ok := extraFMValue(n.extraFM, "date"); ok {
			if t, err := parseForeignTime(v); err == nil {
				n.Created = t.UTC().Truncate(time.Second)
			}
		}
	}
	if n.Created.IsZero() {
		n.Created = fallback
	}

	if n.Updated.IsZero() {
		if v, ok := extraFMValue(n.extraFM, "lastmod"); ok {
			if t, err := parseForeignTime(v); err == nil {
				n.Updated = t.UTC().Truncate(time.Second)
			}
		}
	}
	if n.Updated.IsZero() {
		n.Updated = fallback
	}

	return &Incoming{Note: n}, nil
}

// extraFMValue looks up a top-level "key: value" line in the frontmatter
// lines parseNote didn't recognize, without disturbing them — the caller may
// need to read a value (e.g. Hugo's date:) while leaving it exactly where it
// was for whatever tool put it there.
func extraFMValue(lines []string, key string) (string, bool) {
	for _, line := range lines {
		if fmContinues(line) {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == key {
			return unquote(strings.TrimSpace(v)), true
		}
	}
	return "", false
}

// parseForeignTime accepts the handful of date formats other note tools
// actually write: full RFC3339, a bare "seconds" timestamp with no zone, and
// a date-only value (Hugo's `date: 2026-01-15` is common).
func parseForeignTime(v string) (time.Time, error) {
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q", v)
}

// Import writes one decoded note into the store. It exists as its own method
// because neither Create nor Update fit: Create always stamps Created to now
// and derives the filename from it, and UpdateReq has no Created field — so
// neither can preserve a timestamp that arrived from elsewhere.
func (s *Store) Import(in *Incoming, destFolder string) (*Note, error) {
	if in == nil || in.Note == nil {
		return nil, errors.New("nothing to import")
	}
	destFolder = cleanFolderPath(destFolder)
	if err := validateFolder(destFolder); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	folder := s.canonicalFolderLocked(destFolder)
	if err := s.ensureFolderLocked(folder); err != nil {
		return nil, err
	}

	now := time.Now().UTC().Truncate(time.Second)
	idb := make([]byte, 3)
	rand.Read(idb)
	id := now.Format("20060102-150405") + "-" + hex.EncodeToString(idb) // Create's scheme (notes.go); the sender's id is never reused

	tags := in.Note.Tags
	if tags == nil {
		tags = []string{}
	}
	created := in.Note.Created
	if created.IsZero() {
		created = now
	}
	updated := in.Note.Updated
	if updated.IsZero() {
		updated = created
	}

	n := &Note{
		Meta: Meta{
			ID:      id,
			Title:   in.Note.Title,
			Folder:  folder,
			Tags:    tags,
			Starred: false,
			Trashed: false,
			Created: created,
			Updated: updated,
		},
		Body:    in.Note.Body,
		extraFM: in.Note.extraFM,
	}

	// Attachments get fresh local names (api.go's minting scheme) before the
	// note is written, and every "attachments/<old>" reference in the body
	// is rewritten to match — plain string replacement is safe here because
	// both the old and new names match the fixed timestamp+hex pattern, so
	// there's nothing else in the body they could accidentally collide with.
	if len(in.Attachments) > 0 {
		attachDir := filepath.Join(s.dir, "attachments")
		if err := os.MkdirAll(attachDir, 0o755); err != nil {
			return nil, err
		}
		body := n.Body
		for oldRef, data := range in.Attachments {
			ext, ok := sniffImageExt(data)
			if !ok {
				continue // defensive: ReadBundle already filtered, but never trust it twice
			}
			nb := make([]byte, 4)
			rand.Read(nb)
			newName := now.Format("20060102-150405") + "-" + hex.EncodeToString(nb) + "." + ext
			if err := os.WriteFile(filepath.Join(attachDir, newName), data, 0o644); err != nil {
				return nil, err
			}
			body = strings.ReplaceAll(body, oldRef, bundleAttachDir+newName)
		}
		n.Body = body
	}

	n.file = s.uniqueFileLocked(folder, noteFilename(n.Created, n.Title), id)
	if err := s.write(n); err != nil {
		return nil, err
	}
	s.notes[id] = n
	s.idx.Put(id, n.Title, n.Tags, n.Body)
	cp := *n
	return &cp, nil
}

// RebaseFolders strips the longest common folder prefix shared by every item
// and re-roots what's left under dest, so importing a subtree keeps its
// internal shape instead of flattening: Work/Runbooks/a and Work/Contacts/b
// dropped into Inbox become Inbox/Runbooks/a and Inbox/Contacts/b, not two
// notes both dumped straight into Inbox. A single item has no sibling to
// share a prefix with, so it lands directly in dest.
func RebaseFolders(items []Incoming, dest string) {
	if len(items) == 0 {
		return
	}
	dest = cleanFolderPath(dest)
	destSegs := folderSegs(dest)

	common := folderSegs(items[0].Folder)
	for _, it := range items[1:] {
		common = commonSegPrefix(common, folderSegs(it.Folder))
		if len(common) == 0 {
			break
		}
	}

	for i := range items {
		rest := folderSegs(items[i].Folder)[len(common):]
		segs := append(append([]string{}, destSegs...), rest...)
		items[i].Folder = strings.Join(segs, "/")
	}
}

func folderSegs(f string) []string {
	if f == "" {
		return nil
	}
	return strings.Split(f, "/")
}

func commonSegPrefix(a, b []string) []string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return a[:i]
}
