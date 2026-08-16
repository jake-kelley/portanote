package main

// Note sharing: a .portanote file is a zip holding manifest.json, the notes as
// real .md files, and any images they reference. The same zip travels two ways —
// as a downloaded file, or base64url'd into a one-line code you can paste into a
// chat message. One format, two transports; the share button picks by size.
//
// This file owns only what both halves need to agree on. The writer lives in
// share_export.go, the reader and the foreign-markdown path in share_import.go.

import (
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	bundleFormat  = "portanote-bundle"
	bundleVersion = 1
	bundleExt     = ".portanote"

	manifestPath   = "manifest.json"
	bundleNotesDir = "notes/"
	bundleAttachDir = "attachments/"
)

// Limits applied to anything read from a bundle. A share arrives from someone
// else's machine, so it is treated as hostile input: these bound the damage a
// crafted archive can do before any of it reaches the disk.
const (
	maxEntryBytes  = 25 << 20  // per file, matching the attachment upload cap
	maxBundleBytes = 100 << 20 // total uncompressed, counted while copying (a zip bomb lies in its header)
	maxBundleFiles = 50
)

// Attachments up to this much in total are small enough to ride inside a
// pasteable code; past it the share is offered as a file instead.
const inlineShareMax = 256 << 10

const sharePrefix = "portanote1:"

// Manifest carries what the .md files can't: the format version and, per note,
// the folder it came from and the attachments it uses. Notes ship as real
// markdown so the bundle stays readable without Portanote, and so a recipient
// can unzip and drop the file straight into their notes directory.
type Manifest struct {
	Format  string       `json:"format"`
	V       int          `json:"v"`
	Created time.Time    `json:"created"`
	App     string       `json:"app"`
	Notes   []ManifestNote `json:"notes"`
}

type ManifestNote struct {
	File        string   `json:"file"`        // e.g. "notes/03JULY2026-deploy.md"
	Folder      string   `json:"folder"`      // the sender's folder, a suggestion only
	Attachments []string `json:"attachments"` // e.g. ["attachments/20260815-141203-ab12cd34.png"]
}

// Incoming is one note decoded from a bundle or a markdown file, not yet
// written. Attachments are keyed by the path used in the body, so the importer
// can mint fresh local names and rewrite the references in one pass.
type Incoming struct {
	Note        *Note
	Folder      string // suggested; the operator's choice in the UI wins
	Attachments map[string][]byte
	Warnings    []string // e.g. a body reference with no matching file
}

// attachRefRe finds attachment references in a note body. The names Portanote
// mints are a fixed timestamp+hex pattern, so matching them needs no markdown
// parsing — and the same expression serves the writer (finding what to pack)
// and the importer (rewriting to the new local names).
var attachRefRe = regexp.MustCompile(`attachments/([A-Za-z0-9._-]+)`)

// encodeShareCode renders bundle bytes as one pasteable line.
func encodeShareCode(zipBytes []byte) string {
	return sharePrefix + base64.RawURLEncoding.EncodeToString(zipBytes)
}

// decodeShareCode reverses it. Whitespace anywhere is dropped rather than
// rejected: chat clients and mail wrap long lines, and a code that fails
// because it survived a line break would be a miserable thing to debug.
func decodeShareCode(code string) ([]byte, error) {
	code = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, code)
	if !strings.HasPrefix(code, sharePrefix) {
		return nil, errors.New("not a Portanote share code")
	}
	return base64.RawURLEncoding.DecodeString(strings.TrimPrefix(code, sharePrefix))
}
