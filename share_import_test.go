package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildZip is a small hostile-zip builder for the tests below: it writes
// exactly the bytes given, bypassing BuildBundle so a test can lie about
// sizes, formats, or entry names the way an attacker would.
type zipEntry struct {
	name   string
	data   []byte
	method uint16
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		method := e.method
		if method == 0 {
			method = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validManifest(notes []ManifestNote) []byte {
	m := Manifest{Format: bundleFormat, V: bundleVersion, App: "test"}
	m.Notes = notes
	data, _ := json.MarshalIndent(m, "", "  ")
	return data
}

// tinyPNG is a 1x1 transparent PNG, enough for http.DetectContentType to
// sniff it as image/png.
var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func mustReadBundle(t *testing.T, data []byte) []Incoming {
	t.Helper()
	items, err := ReadBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// ---------------------------------------------------------------- hostile input

func TestReadBundleZipSlip(t *testing.T) {
	notes := []ManifestNote{
		{File: "../../evil.md", Folder: ""},
	}
	data := buildZip(t, []zipEntry{
		{name: manifestPath, data: validManifest(notes)},
		{name: "../../evil.md", data: []byte("---\ntitle: \"Evil\"\n---\n\nbody\n")},
		{name: "attachments/../../evil.png", data: tinyPNG},
	})
	// "../../evil.md" isn't under "notes/", so it's ignored, which means the
	// manifest's own reference to it can't be satisfied either.
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: manifest points at an entry outside notes/")
	}

	// The attachment path-traversal attempt must never escape into a real
	// path — names are map keys only, checked by making sure a legitimate
	// bundle sharing the same archive still imports cleanly with no file
	// written outside the notes dir.
	s, dir := newTestStore(t)
	good := buildZip(t, []zipEntry{
		{name: manifestPath, data: validManifest([]ManifestNote{{File: "notes/x.md", Folder: ""}})},
		{name: "notes/x.md", data: []byte("---\ntitle: \"X\"\n---\n\nSee attachments/../../evil.png\n")},
		{name: "attachments/../../evil.png", data: tinyPNG},
	})
	items := mustReadBundle(t, good)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if len(items[0].Attachments) != 0 {
		t.Fatalf("zip-slip attachment name should never resolve to an entry, got %v", items[0].Attachments)
	}
	if _, err := s.Import(&items[0], ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.png")); err == nil {
		t.Fatal("zip-slip attachment escaped the notes dir")
	}
}

func TestReadBundleZipBombPerEntry(t *testing.T) {
	// Declares a small uncompressed size in effect but the reader must not
	// trust it anyway: stream real bytes past maxEntryBytes and confirm it's
	// refused instead of silently truncated or accepted.
	big := bytes.Repeat([]byte("a"), maxEntryBytes+1024)
	data := buildZip(t, []zipEntry{
		{name: manifestPath, data: validManifest([]ManifestNote{{File: "notes/x.md"}})},
		{name: "notes/x.md", data: big},
	})
	_, err := ReadBundle(data)
	if err == nil {
		t.Fatal("expected an error for an entry past maxEntryBytes")
	}
}

func TestReadBundleZipBombTotal(t *testing.T) {
	var entries []zipEntry
	var notes []ManifestNote
	chunk := bytes.Repeat([]byte("b"), maxEntryBytes)
	// Each individual entry is within the per-file limit, but enough of them
	// blow past maxBundleBytes in total.
	n := int(maxBundleBytes/maxEntryBytes) + 2
	for i := 0; i < n; i++ {
		name := "notes/" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".md"
		entries = append(entries, zipEntry{name: name, data: chunk})
		notes = append(notes, ManifestNote{File: name})
	}
	entries = append(entries, zipEntry{name: manifestPath, data: validManifest(notes)})
	data := buildZip(t, entries)
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: total bundle size exceeds maxBundleBytes")
	}
}

func TestReadBundleTooManyFiles(t *testing.T) {
	var entries []zipEntry
	for i := 0; i < maxBundleFiles+5; i++ {
		entries = append(entries, zipEntry{name: "attachments/junk" + string(rune('a'+i%26)) + ".txt", data: []byte("x")})
	}
	entries = append(entries, zipEntry{name: manifestPath, data: validManifest(nil)})
	data := buildZip(t, entries)
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: too many entries")
	}
}

func TestReadBundleRejectsFakeImage(t *testing.T) {
	notes := []ManifestNote{{File: "notes/x.md", Attachments: []string{"attachments/evil.png"}}}
	data := buildZip(t, []zipEntry{
		{name: manifestPath, data: validManifest(notes)},
		{name: "notes/x.md", data: []byte("---\ntitle: \"X\"\n---\n\nattachments/evil.png\n")},
		{name: "attachments/evil.png", data: []byte("#!/bin/sh\necho pwned\n")},
	})
	items := mustReadBundle(t, data)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if len(items[0].Attachments) != 0 {
		t.Errorf("fake image should not have been accepted as an attachment: %v", items[0].Attachments)
	}
	if len(items[0].Warnings) == 0 {
		t.Error("expected a warning about the missing/rejected attachment")
	}
}

func TestReadBundleMissingManifest(t *testing.T) {
	data := buildZip(t, []zipEntry{{name: "notes/x.md", data: []byte("hi")}})
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: no manifest.json")
	}
}

func TestReadBundleCorruptManifest(t *testing.T) {
	data := buildZip(t, []zipEntry{{name: manifestPath, data: []byte("{not json")}})
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: corrupt manifest")
	}
}

func TestReadBundleWrongFormat(t *testing.T) {
	m := Manifest{Format: "something-else", V: bundleVersion}
	mj, _ := json.Marshal(m)
	data := buildZip(t, []zipEntry{{name: manifestPath, data: mj}})
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: wrong format")
	}
}

func TestReadBundleFutureVersion(t *testing.T) {
	m := Manifest{Format: bundleFormat, V: 999}
	mj, _ := json.Marshal(m)
	data := buildZip(t, []zipEntry{{name: manifestPath, data: mj}})
	if _, err := ReadBundle(data); err == nil {
		t.Fatal("expected an error: unknown future version")
	}
}

func TestReadBundleIgnoresUnknownEntry(t *testing.T) {
	notes := []ManifestNote{{File: "notes/x.md"}}
	data := buildZip(t, []zipEntry{
		{name: manifestPath, data: validManifest(notes)},
		{name: "notes/x.md", data: []byte("---\ntitle: \"X\"\n---\n\nbody\n")},
		{name: "future/v2-thing.bin", data: []byte("whatever v2 needs")},
	})
	items := mustReadBundle(t, data)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Note.Title != "X" {
		t.Errorf("Title = %q, want %q", items[0].Note.Title, "X")
	}
}

// ---------------------------------------------------------------- Import

func TestImportMintsFreshIDAndPreservesTimestamps(t *testing.T) {
	s, _ := newTestStore(t)
	created := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	updated := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	in := &Incoming{
		Note: &Note{
			Meta: Meta{ID: "senders-id", Title: "Imported Note", Created: created, Updated: updated},
			Body: "hello",
		},
	}
	n, err := s.Import(in, "Inbox")
	if err != nil {
		t.Fatal(err)
	}
	if n.ID == "senders-id" || n.ID == "" {
		t.Errorf("ID = %q, should be freshly minted", n.ID)
	}
	if !n.Created.Equal(created) {
		t.Errorf("Created = %v, want %v", n.Created, created)
	}
	if !n.Updated.Equal(updated) {
		t.Errorf("Updated = %v, want %v", n.Updated, updated)
	}
	if n.Folder != "Inbox" {
		t.Errorf("Folder = %q, want %q", n.Folder, "Inbox")
	}
	got, err := s.Get(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Imported Note" {
		t.Errorf("stored Title = %q", got.Title)
	}
}

func TestImportRejectsReservedFolder(t *testing.T) {
	s, _ := newTestStore(t)
	in := &Incoming{Note: &Note{Meta: Meta{Title: "X"}, Body: "b"}}
	if _, err := s.Import(in, "attachments"); err == nil {
		t.Fatal("expected an error importing into a reserved folder")
	}
}

func TestImportRewritesAttachmentReferences(t *testing.T) {
	s, dir := newTestStore(t)
	in := &Incoming{
		Note: &Note{
			Meta: Meta{Title: "Screenshot"},
			Body: "see attachments/old-name.png here",
		},
		Attachments: map[string][]byte{"attachments/old-name.png": tinyPNG},
	}
	n, err := s.Import(in, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(n.Body, "old-name.png") {
		t.Errorf("body still references the old attachment name: %q", n.Body)
	}
	if !strings.Contains(n.Body, "attachments/") {
		t.Errorf("body lost its attachment reference entirely: %q", n.Body)
	}
	// Confirm the new file actually landed on disk.
	entries, err := os.ReadDir(filepath.Join(dir, "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files in attachments/, want 1", len(entries))
	}
	if entries[0].Name() == "old-name.png" {
		t.Error("attachment kept its old name instead of getting a fresh one")
	}
	if !strings.Contains(n.Body, entries[0].Name()) {
		t.Errorf("body doesn't reference the new attachment name %q: %q", entries[0].Name(), n.Body)
	}
}

func TestImportTitleClashDoesNotOverwrite(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Create("Same Title"); err != nil {
		t.Fatal(err)
	}
	in := &Incoming{Note: &Note{Meta: Meta{Title: "Same Title"}, Body: "second"}}
	n2, err := s.Import(in, "")
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	count := 0
	for _, it := range list {
		if it.Title == "Same Title" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected both notes to survive, found %d with the title", count)
	}
	got, err := s.Get(n2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "second" {
		t.Errorf("Body = %q, want %q", got.Body, "second")
	}
}

// ---------------------------------------------------------------- ParseMarkdownImport

func TestParseMarkdownImportBareFile(t *testing.T) {
	mtime := time.Date(2019, 5, 1, 12, 0, 0, 0, time.UTC)
	raw := "# My Title\n\nSome body text.\n"
	in, err := ParseMarkdownImport("random.md", []byte(raw), mtime)
	if err != nil {
		t.Fatal(err)
	}
	if in.Note.Title != "My Title" {
		t.Errorf("Title = %q, want %q", in.Note.Title, "My Title")
	}
	if in.Note.Created.IsZero() {
		t.Fatal("Created should not be zero")
	}
	if !in.Note.Created.Equal(mtime) {
		t.Errorf("Created = %v, want mtime %v", in.Note.Created, mtime)
	}
	fn := noteFilename(in.Note.Created, in.Note.Title)
	if strings.HasPrefix(fn, "01JANUARY0001") {
		t.Fatalf("filename derived from zero time: %q", fn)
	}
}

func TestParseMarkdownImportHugoDate(t *testing.T) {
	raw := "---\n" +
		"title: \"Hugo Post\"\n" +
		"date: 2022-06-15T10:00:00Z\n" +
		"---\n\nBody.\n"
	in, err := ParseMarkdownImport("post.md", []byte(raw), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2022, 6, 15, 10, 0, 0, 0, time.UTC)
	if !in.Note.Created.Equal(want) {
		t.Errorf("Created = %v, want %v", in.Note.Created, want)
	}
	// date: must survive in extraFM, unconsumed, so Hugo still sees it.
	found := false
	for _, line := range in.Note.extraFM {
		if strings.HasPrefix(line, "date:") {
			found = true
		}
	}
	if !found {
		t.Error("date: frontmatter line was consumed instead of left for Hugo")
	}
}

func TestParseMarkdownImportClearsID(t *testing.T) {
	raw := "---\nid: \"some-id\"\ntitle: \"T\"\n---\n\nbody\n"
	in, err := ParseMarkdownImport("x.md", []byte(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if in.Note.ID != "" {
		t.Errorf("ID = %q, want cleared", in.Note.ID)
	}
}

// ---------------------------------------------------------------- RebaseFolders

func TestRebaseFoldersStripsCommonPrefix(t *testing.T) {
	items := []Incoming{
		{Note: &Note{}, Folder: "Work/Runbooks"},
		{Note: &Note{}, Folder: "Work/Contacts"},
	}
	RebaseFolders(items, "Inbox")
	if items[0].Folder != "Inbox/Runbooks" {
		t.Errorf("items[0].Folder = %q, want %q", items[0].Folder, "Inbox/Runbooks")
	}
	if items[1].Folder != "Inbox/Contacts" {
		t.Errorf("items[1].Folder = %q, want %q", items[1].Folder, "Inbox/Contacts")
	}
}

func TestRebaseFoldersSingleItem(t *testing.T) {
	items := []Incoming{{Note: &Note{}, Folder: "Deeply/Nested/Folder"}}
	RebaseFolders(items, "Inbox")
	if items[0].Folder != "Inbox" {
		t.Errorf("Folder = %q, want %q", items[0].Folder, "Inbox")
	}
}

func TestRebaseFoldersNoCommonPrefix(t *testing.T) {
	items := []Incoming{
		{Note: &Note{}, Folder: "Alpha"},
		{Note: &Note{}, Folder: "Beta"},
	}
	RebaseFolders(items, "Inbox")
	if items[0].Folder != "Inbox/Alpha" || items[1].Folder != "Inbox/Beta" {
		t.Errorf("got %q, %q", items[0].Folder, items[1].Folder)
	}
}

// ---------------------------------------------------------------- round trip

func TestBundleRoundTrip(t *testing.T) {
	src, srcDir := newTestStore(t)
	if err := os.MkdirAll(filepath.Join(srcDir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "attachments", "pic.png"), tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := src.Create("Round Trip Title")
	if err != nil {
		t.Fatal(err)
	}
	body := "Body with an image: attachments/pic.png"
	tags := []string{"alpha", "beta"}
	n = mustUpdate(t, src, n.ID, UpdateReq{Body: &body, Tags: &tags})

	b, err := BuildBundle(src, []string{n.ID})
	if err != nil {
		t.Fatal(err)
	}

	items, err := ReadBundle(b.Zip)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	in := items[0]
	if in.Note.Title != "Round Trip Title" {
		t.Errorf("Title = %q", in.Note.Title)
	}
	if len(in.Note.Tags) != 2 {
		t.Errorf("Tags = %v", in.Note.Tags)
	}
	if !in.Note.Created.Equal(n.Created) {
		t.Errorf("Created = %v, want %v", in.Note.Created, n.Created)
	}
	if len(in.Attachments) != 1 {
		t.Fatalf("Attachments = %d, want 1", len(in.Attachments))
	}

	dst, dstDir := newTestStore(t)
	got, err := dst.Import(&in, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Round Trip Title" {
		t.Errorf("imported Title = %q", got.Title)
	}
	if !strings.Contains(got.Body, "attachments/") || strings.Contains(got.Body, "pic.png") {
		t.Errorf("imported Body should reference a freshly named attachment, got %q", got.Body)
	}
	entries, err := os.ReadDir(filepath.Join(dstDir, "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d attachment files in destination store, want 1", len(entries))
	}
}
