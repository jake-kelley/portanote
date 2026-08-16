package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openBundle(t *testing.T, b *BuiltBundle) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b.Zip), int64(len(b.Zip)))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func readZipFile(t *testing.T, zr *zip.Reader, name string) []byte {
	t.Helper()
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			data, err := io.ReadAll(rc)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("zip has no entry %q", name)
	return nil
}

func findZipEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func TestBuildBundleStructure(t *testing.T) {
	s, _ := newTestStore(t)
	n, err := s.Create("Deploy Notes")
	if err != nil {
		t.Fatal(err)
	}

	b, err := BuildBundle(s, []string{n.ID})
	if err != nil {
		t.Fatal(err)
	}
	if b.Notes != 1 {
		t.Fatalf("Notes = %d, want 1", b.Notes)
	}

	zr := openBundle(t, b)
	mf := findZipEntry(zr, "manifest.json")
	if mf == nil {
		t.Fatal("missing manifest.json")
	}
	rc, err := mf.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var m Manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.Format != bundleFormat {
		t.Errorf("Format = %q, want %q", m.Format, bundleFormat)
	}
	if m.V != bundleVersion {
		t.Errorf("V = %d, want %d", m.V, bundleVersion)
	}
	if len(m.Notes) != 1 {
		t.Fatalf("manifest has %d notes, want 1", len(m.Notes))
	}
	if findZipEntry(zr, m.Notes[0].File) == nil {
		t.Errorf("manifest points at %q, not present in zip", m.Notes[0].File)
	}
	if !strings.HasPrefix(m.Notes[0].File, bundleNotesDir) {
		t.Errorf("note file %q not under %q", m.Notes[0].File, bundleNotesDir)
	}
}

func TestBuildBundleMarkdownStripsSenderOnlyFields(t *testing.T) {
	s, _ := newTestStore(t)
	n, err := s.Create("Runbook")
	if err != nil {
		t.Fatal(err)
	}
	starred := true
	trashed := true
	mustUpdate(t, s, n.ID, UpdateReq{Starred: &starred, Trashed: &trashed})

	b, err := BuildBundle(s, []string{n.ID})
	if err != nil {
		t.Fatal(err)
	}
	zr := openBundle(t, b)
	var mdName string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, bundleNotesDir) {
			mdName = f.Name
		}
	}
	md := string(readZipFile(t, zr, mdName))

	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "id:") {
			t.Errorf("md kept an id: line: %q", line)
		}
		if strings.HasPrefix(line, "starred:") {
			t.Errorf("md kept a starred: line: %q", line)
		}
	}
	if !strings.Contains(md, "trashed: false") {
		t.Error("md should force trashed: false")
	}
	if !strings.Contains(md, "created:") || !strings.Contains(md, "updated:") {
		t.Error("md should keep created/updated")
	}
}

func TestBuildBundlePreservesForeignFrontmatter(t *testing.T) {
	dir := t.TempDir()
	raw := "---\n" +
		"id: \"myid\"\n" +
		"title: \"Foreign\"\n" +
		"tags: []\n" +
		"starred: false\n" +
		"trashed: false\n" +
		"created: 2026-01-01T00:00:00Z\n" +
		"updated: 2026-01-01T00:00:00Z\n" +
		"type: Runbook\n" +
		"---\n\nBody text.\n"
	if err := os.WriteFile(filepath.Join(dir, "myid.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	b, err := BuildBundle(s, []string{"myid"})
	if err != nil {
		t.Fatal(err)
	}
	zr := openBundle(t, b)
	var mdName string
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, bundleNotesDir) {
			mdName = f.Name
		}
	}
	md := string(readZipFile(t, zr, mdName))
	if !strings.Contains(md, "type: Runbook") {
		t.Errorf("foreign frontmatter did not survive bundling: %q", md)
	}
}

func TestBuildBundlePacksAttachmentsAndSkipsMissing(t *testing.T) {
	s, dir := newTestStore(t)
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", "a.png"), []byte("PNGDATA1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", "b.png"), []byte("PNGDATA22"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := s.Create("Screens")
	if err != nil {
		t.Fatal(err)
	}
	body := "See attachments/a.png and attachments/b.png and attachments/missing.png"
	mustUpdate(t, s, n.ID, UpdateReq{Body: &body})

	b, err := BuildBundle(s, []string{n.ID})
	if err != nil {
		t.Fatal(err)
	}
	if b.Attachments != 2 {
		t.Fatalf("Attachments = %d, want 2", b.Attachments)
	}

	zr := openBundle(t, b)
	for _, name := range []string{"a.png", "b.png"} {
		f := findZipEntry(zr, bundleAttachDir+name)
		if f == nil {
			t.Fatalf("missing packed attachment %q", name)
		}
		if f.Method != zip.Store {
			t.Errorf("attachment %q packed with method %d, want zip.Store (%d)", name, f.Method, zip.Store)
		}
	}
	if findZipEntry(zr, bundleAttachDir+"missing.png") != nil {
		t.Error("missing.png should have been skipped, not packed")
	}
}

func TestBuildBundleDedupesSharedAttachment(t *testing.T) {
	s, dir := newTestStore(t)
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", "shared.png"), []byte("SHARED"), 0o644); err != nil {
		t.Fatal(err)
	}

	n1, err := s.Create("One")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := s.Create("Two")
	if err != nil {
		t.Fatal(err)
	}
	body := "attachments/shared.png"
	mustUpdate(t, s, n1.ID, UpdateReq{Body: &body})
	mustUpdate(t, s, n2.ID, UpdateReq{Body: &body})

	b, err := BuildBundle(s, []string{n1.ID, n2.ID})
	if err != nil {
		t.Fatal(err)
	}
	if b.Attachments != 1 {
		t.Fatalf("Attachments = %d, want 1 (shared file packed once)", b.Attachments)
	}

	zr := openBundle(t, b)
	count := 0
	for _, f := range zr.File {
		if f.Name == bundleAttachDir+"shared.png" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared.png appears %d times in the zip, want 1", count)
	}
}

func TestBuildBundleUseCode(t *testing.T) {
	s, dir := newTestStore(t)
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}

	small, err := s.Create("Small")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", "small.png"), []byte("tiny"), 0o644); err != nil {
		t.Fatal(err)
	}
	smallBody := "attachments/small.png"
	mustUpdate(t, s, small.ID, UpdateReq{Body: &smallBody})

	b, err := BuildBundle(s, []string{small.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !b.UseCode {
		t.Errorf("UseCode = false for a %d-byte attachment total, want true", b.AttachBytes)
	}

	big, err := s.Create("Big")
	if err != nil {
		t.Fatal(err)
	}
	bigData := bytes.Repeat([]byte("x"), inlineShareMax+1)
	if err := os.WriteFile(filepath.Join(dir, "attachments", "big.png"), bigData, 0o644); err != nil {
		t.Fatal(err)
	}
	bigBody := "attachments/big.png"
	mustUpdate(t, s, big.ID, UpdateReq{Body: &bigBody})

	b2, err := BuildBundle(s, []string{big.ID})
	if err != nil {
		t.Fatal(err)
	}
	if b2.UseCode {
		t.Errorf("UseCode = true for a %d-byte attachment total (max %d), want false", b2.AttachBytes, inlineShareMax)
	}
}

func TestShareCodeRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)
	n, err := s.Create("Code Test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildBundle(s, []string{n.ID})
	if err != nil {
		t.Fatal(err)
	}

	code := encodeShareCode(b.Zip)
	got, err := decodeShareCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, b.Zip) {
		t.Fatal("round-tripped zip bytes differ from the original")
	}

	// Chat clients wrap long lines; whitespace inserted mid-code must not
	// break the decode.
	var wrapped strings.Builder
	for i, r := range code {
		wrapped.WriteRune(r)
		if i%40 == 39 {
			wrapped.WriteString("\n")
		}
	}
	got2, err := decodeShareCode(wrapped.String())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got2, b.Zip) {
		t.Fatal("round-trip through a line-wrapped code differs from the original")
	}
}
