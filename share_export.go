package main

// The writer half of the .portanote bundle format (see share.go for the
// shared contract). BuildBundle turns a set of note IDs into a zip: a
// manifest, the notes as real .md files, and the images they reference.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BuiltBundle is the result of packing notes into a .portanote zip, plus
// enough bookkeeping for the caller to decide how to hand it to the
// recipient (download vs. a pasteable share code).
type BuiltBundle struct {
	Zip         []byte
	Filename    string // e.g. "guardduty-ec2-quarantine.portanote", from slugify()+bundleExt
	Notes       int
	Attachments int
	AttachBytes int  // total uncompressed attachment bytes, before compression
	UseCode     bool // AttachBytes <= inlineShareMax
}

// BuildBundle packs the given notes, and every attachment they reference,
// into a single .portanote zip.
func BuildBundle(s *Store, ids []string) (*BuiltBundle, error) {
	s.mu.RLock()
	notes := make([]*Note, 0, len(ids))
	for _, id := range ids {
		if n, ok := s.notes[id]; ok {
			cp := *n
			notes = append(notes, &cp)
		}
	}
	dir := s.dir
	s.mu.RUnlock()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	manifest := Manifest{
		Format:  bundleFormat,
		V:       bundleVersion,
		Created: time.Now().UTC(),
		App:     "portanote " + version,
	}

	// Attachments are keyed by their body-reference name (e.g. the hex
	// filename after "attachments/") so the same image referenced by two
	// notes is only ever read and packed once.
	packed := map[string]bool{}
	var attachCount, attachBytes int

	for _, n := range notes {
		mdName := path.Base(n.file) + ".md"
		mdPath := bundleNotesDir + mdName

		refs := attachRefsIn(n.Body)
		var noteAttach []string
		for _, name := range refs {
			ref := bundleAttachDir + name
			if !packed[name] {
				data, err := os.ReadFile(filepath.Join(dir, "attachments", name))
				if err != nil {
					// Missing on disk: skip silently, the sender's note
					// outlived the file, or it was never uploaded.
					continue
				}
				w, err := zw.CreateHeader(&zip.FileHeader{
					Name:   ref,
					Method: zip.Store, // already-compressed image formats: deflating just burns CPU
				})
				if err != nil {
					zw.Close()
					return nil, err
				}
				if _, err := w.Write(data); err != nil {
					zw.Close()
					return nil, err
				}
				packed[name] = true
				attachCount++
				attachBytes += len(data)
			}
			noteAttach = append(noteAttach, ref)
		}

		md := bundleMarkdown(n)
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   mdPath,
			Method: zip.Deflate,
		})
		if err != nil {
			zw.Close()
			return nil, err
		}
		if _, err := w.Write([]byte(md)); err != nil {
			zw.Close()
			return nil, err
		}

		manifest.Notes = append(manifest.Notes, ManifestNote{
			File:        mdPath,
			Folder:      n.Folder,
			Attachments: noteAttach,
		})
	}

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		zw.Close()
		return nil, err
	}
	mw, err := zw.CreateHeader(&zip.FileHeader{
		Name:   manifestPath,
		Method: zip.Deflate,
	})
	if err != nil {
		zw.Close()
		return nil, err
	}
	if _, err := mw.Write(manifestJSON); err != nil {
		zw.Close()
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	name := bundleFilename(notes)
	return &BuiltBundle{
		Zip:         buf.Bytes(),
		Filename:    name,
		Notes:       len(notes),
		Attachments: attachCount,
		AttachBytes: attachBytes,
		UseCode:     attachBytes <= inlineShareMax,
	}, nil
}

// bundleFilename names the download after the first note's title, falling
// back to something generic for an empty or unslugifiable selection — a
// multi-note share still needs one filename.
func bundleFilename(notes []*Note) string {
	if len(notes) > 0 {
		if slug := slugify(notes[0].Title); slug != "" {
			return slug + bundleExt
		}
	}
	return "notes" + bundleExt
}

// attachRefsIn returns the distinct attachment names (without the
// "attachments/" prefix) a note body refers to, in first-seen order —
// matching order keeps bundle output deterministic for tests.
func attachRefsIn(body string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range attachRefRe.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
}

// bundleMarkdown renders a note the way it should travel: no id (the
// recipient's parseNote falls back to the basename, so a preserved id would
// only risk colliding with the sender's), never trashed (importing straight
// into someone's trash would be baffling), and no starred (that's the
// sender's preference, not the recipient's). created/updated/tags and any
// foreign frontmatter travel verbatim — see the comment at notes.go:604-608.
func bundleMarkdown(n *Note) string {
	cp := *n
	cp.Starred = false
	cp.Trashed = false
	raw := serializeNote(&cp)
	lines := strings.Split(raw, "\n")
	out := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(line, "id: ") || strings.HasPrefix(line, "starred: ") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
