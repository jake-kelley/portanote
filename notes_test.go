package main

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func mustUpdate(t *testing.T, s *Store, id string, req UpdateReq) *Note {
	t.Helper()
	n, err := s.Update(id, req)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func strp(v string) *string { return &v }

// Frontmatter written by another tool survives Portanote editing the note:
// notes are just files, so keys Portanote doesn't own must come back verbatim.
func TestFrontmatterPreservesForeignKeys(t *testing.T) {
	dir := t.TempDir()
	raw := "---\r\n" + // CRLF: a note edited on Windows by another editor
		"# who wrote this\r\n" +
		"type: Runbook\r\n" +
		"title: Restart the Ingress\r\n" +
		"description: How to safely restart the ingress controller.\r\n" +
		"aliases:\r\n" +
		"  - ingress-restart\r\n" +
		"  - bounce-ingress\r\n" +
		"review:\r\n" +
		"  by: jake\r\n" +
		"  every: 90d\r\n" +
		"tags: [k8s, ingress]\r\n" +
		"starred: false\r\n" +
		"---\r\n\r\n# Restart the Ingress\r\n"
	if err := os.WriteFile(filepath.Join(dir, "restart-ingress.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Portanote's own keys are still parsed, not swept into the kept lines
	n := s.List()[0]
	if n.Title != "Restart the Ingress" || strings.Join(n.Tags, ",") != "k8s,ingress" || n.Starred {
		t.Fatalf("owned keys mis-parsed: %+v", n)
	}

	mustUpdate(t, s, n.ID, UpdateReq{Starred: boolp(true)}) // any ordinary edit rewrites the file
	out := readNoteFile(t, dir, n.ID)

	for _, want := range []string{
		"# who wrote this",
		"type: Runbook",
		"description: How to safely restart the ingress controller.",
		"aliases:",
		"  - ingress-restart",
		"  - bounce-ingress",
		"review:",
		"  by: jake",
		"  every: 90d",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("save dropped %q from the frontmatter:\n%s", want, out)
		}
	}
	// the edit still landed, and Portanote's fields aren't duplicated
	if !strings.Contains(out, "starred: true") || strings.Count(out, "starred:") != 1 {
		t.Errorf("owned key not rewritten cleanly:\n%s", out)
	}
	if strings.Count(out, "type: Runbook") != 1 {
		t.Errorf("kept key duplicated on save:\n%s", out)
	}
	// and it all survives a second round trip (reload from disk, save again)
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, s2, n.ID, UpdateReq{Body: strp("edited\n")})
	if out2 := readNoteFile(t, dir, n.ID); !strings.Contains(out2, "type: Runbook") ||
		!strings.Contains(out2, "  by: jake") {
		t.Errorf("kept keys lost on the second round trip:\n%s", out2)
	}
}

// Block-form tags (what Obsidian writes by default) are read, not emptied.
func TestFrontmatterBlockFormTags(t *testing.T) {
	dir := t.TempDir()
	raw := "---\ntitle: Obsidian Style\ntags:\n  - k8s\n  - \"quoted tag\"\ntype: Runbook\n---\n\n# Obsidian Style\n"
	if err := os.WriteFile(filepath.Join(dir, "obsidian-style.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := s.List()[0]
	if got := strings.Join(n.Tags, "|"); got != "k8s|quoted tag" {
		t.Fatalf("block-form tags = %q, want k8s|quoted tag", got)
	}
	// the list items belong to tags, so they must not leak into the kept lines
	mustUpdate(t, s, n.ID, UpdateReq{Starred: boolp(true)})
	out := readNoteFile(t, dir, n.ID)
	if !strings.Contains(out, "tags: [k8s, quoted tag]") {
		t.Errorf("tags not rewritten:\n%s", out)
	}
	if strings.Contains(out, "- k8s") || strings.Count(out, "k8s") != 1 {
		t.Errorf("tag items duplicated into the kept frontmatter:\n%s", out)
	}
	if !strings.Contains(out, "type: Runbook") {
		t.Errorf("foreign key after the tags block was lost:\n%s", out)
	}
}

// A note Portanote wrote itself has no foreign keys and gains no blank padding.
func TestFrontmatterCleanForOwnNotes(t *testing.T) {
	s, dir := newTestStore(t)
	n, err := s.Create("Plain Note")
	if err != nil {
		t.Fatal(err)
	}
	out := readNoteFile(t, dir, n.ID)
	fm, _, _ := strings.Cut(strings.TrimPrefix(out, "---\n"), "---\n")
	for _, line := range strings.Split(strings.TrimSuffix(fm, "\n"), "\n") {
		key, _, _ := strings.Cut(line, ":")
		if !knownFM[strings.TrimSpace(key)] {
			t.Errorf("unexpected line %q in a fresh note's frontmatter:\n%s", line, fm)
		}
	}
}

func boolp(v bool) *bool { return &v }

// readNoteFile finds the note's file on disk (the name tracks folder+title) and
// returns its contents.
func readNoteFile(t *testing.T, dir, id string) string {
	t.Helper()
	var found string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		if raw, err := os.ReadFile(p); err == nil && strings.Contains(string(raw), `id: "`+id+`"`) {
			found = string(raw)
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no file on disk for note %s", id)
	}
	return found
}

// A hand-made subdirectory with a plain .md file is adopted as a folder.
func TestScanAdoptsSubdirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "projects", "project1.md"),
		[]byte("# Project One\n\nnotes here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	items := s.List()
	if len(items) != 1 {
		t.Fatalf("got %d notes, want 1", len(items))
	}
	if items[0].Folder != "projects" {
		t.Errorf("folder = %q, want %q", items[0].Folder, "projects")
	}
	if items[0].Title != "Project One" {
		t.Errorf("title = %q, want %q", items[0].Title, "Project One")
	}
	found := false
	for _, f := range s.Folders() {
		if f.Name == "projects" {
			found = true
			if f.Count != 1 {
				t.Errorf("folder count = %d, want 1", f.Count)
			}
		}
	}
	if !found {
		t.Errorf("folder %q not listed: %+v", "projects", s.Folders())
	}
}

// Setting a note's folder moves its file into that folder's directory.
func TestUpdateMovesFileIntoFolderDir(t *testing.T) {
	s, dir := newTestStore(t)
	n, err := s.Create("My Note")
	if err != nil {
		t.Fatal(err)
	}
	base := noteFilename(n.Created, "My Note")
	if _, err := os.Stat(filepath.Join(dir, base+".md")); err != nil {
		t.Fatalf("note not at root after create: %v", err)
	}

	moved := mustUpdate(t, s, n.ID, UpdateReq{Folder: strp("Work/Runbooks")})
	if moved.Folder != "Work/Runbooks" {
		t.Fatalf("folder = %q", moved.Folder)
	}
	if _, err := os.Stat(filepath.Join(dir, "Work", "Runbooks", base+".md")); err != nil {
		t.Errorf("file not moved into folder dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, base+".md")); !os.IsNotExist(err) {
		t.Error("old root file still present after move")
	}

	// moving back out returns the file to the root
	mustUpdate(t, s, n.ID, UpdateReq{Folder: strp("")})
	if _, err := os.Stat(filepath.Join(dir, base+".md")); err != nil {
		t.Errorf("file not back at root: %v", err)
	}
}

// Old flat layout (frontmatter folder + JSON manifest) migrates to real dirs.
func TestLegacyLayoutMigration(t *testing.T) {
	dir := t.TempDir()
	legacy := "---\n" +
		"id: \"legacy-1\"\n" +
		"title: \"Legacy Note\"\n" +
		"folder: \"Work/Sub\"\n" +
		"tags: [aws]\n" +
		"starred: false\n" +
		"trashed: false\n" +
		"created: 2026-01-02T03:04:05Z\n" +
		"updated: 2026-01-02T03:04:05Z\n" +
		"---\n\nbody text\n"
	if err := os.WriteFile(filepath.Join(dir, "02JANUARY2026-legacy-note.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".portanote-folders.json"),
		[]byte(`{"folders":["Work/Sub","Empty/Deep"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.Get("legacy-1")
	if err != nil {
		t.Fatal(err)
	}
	if n.Folder != "Work/Sub" || n.Body != "body text\n" {
		t.Fatalf("migrated note = %+v", n)
	}
	movedPath := filepath.Join(dir, "Work", "Sub", "02JANUARY2026-legacy-note.md")
	raw, err := os.ReadFile(movedPath)
	if err != nil {
		t.Fatalf("file not moved into folder dir: %v", err)
	}
	if strings.Contains(string(raw), "folder:") {
		t.Error("frontmatter still carries the legacy folder field")
	}
	if _, err := os.Stat(filepath.Join(dir, "02JANUARY2026-legacy-note.md")); !os.IsNotExist(err) {
		t.Error("old root file still present after migration")
	}
	if _, err := os.Stat(filepath.Join(dir, ".portanote-folders.json")); !os.IsNotExist(err) {
		t.Error("legacy folders manifest not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "Empty", "Deep")); err != nil {
		t.Errorf("empty folder from manifest not created as a directory: %v", err)
	}

	// a restart keeps everything (and doesn't adopt seeded templates as notes)
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Count() != 1 {
		t.Fatalf("after restart got %d notes, want 1", s2.Count())
	}
	n2, err := s2.Get("legacy-1")
	if err != nil || n2.Folder != "Work/Sub" {
		t.Fatalf("after restart: %+v, %v", n2, err)
	}
	hasEmpty := false
	for _, f := range s2.Folders() {
		if f.Name == "Empty/Deep" {
			hasEmpty = true
		}
	}
	if !hasEmpty {
		t.Errorf("empty folder lost on restart: %+v", s2.Folders())
	}
}

// Renaming a folder renames the directory; files travel with it.
func TestRenameFolderMovesDirectory(t *testing.T) {
	s, dir := newTestStore(t)
	n, _ := s.Create("Deep Note")
	mustUpdate(t, s, n.ID, UpdateReq{Folder: strp("Old/Deep")})

	if err := s.RenameFolder("Old", "New"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "New", "Deep")); err != nil {
		t.Errorf("renamed dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Old")); !os.IsNotExist(err) {
		t.Error("old dir still present")
	}
	got, _ := s.Get(n.ID)
	if got.Folder != "New/Deep" {
		t.Errorf("note folder = %q, want New/Deep", got.Folder)
	}
	// the note is still editable at its new location
	upd := mustUpdate(t, s, n.ID, UpdateReq{Body: strp("hello")})
	if _, err := os.Stat(filepath.Join(dir, "New", "Deep", path.Base(upd.file)+".md")); err != nil {
		t.Errorf("note file not writable at new location: %v", err)
	}

	// renaming onto an existing folder is refused (directories don't merge)
	s.CreateFolder("Third")
	if err := s.RenameFolder("New", "Third"); err == nil {
		t.Error("rename onto an existing folder should fail")
	}
}

// Deleting a folder uncategorizes its notes and removes the directories.
func TestDeleteFolderKeepsNotes(t *testing.T) {
	s, dir := newTestStore(t)
	n, _ := s.Create("Survivor")
	mustUpdate(t, s, n.ID, UpdateReq{Folder: strp("Zed/Sub")})

	if err := s.DeleteFolder("Zed"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Folder != "" {
		t.Errorf("note folder = %q, want empty", got.Folder)
	}
	base := noteFilename(n.Created, "Survivor")
	if _, err := os.Stat(filepath.Join(dir, base+".md")); err != nil {
		t.Errorf("note file not back at root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Zed")); !os.IsNotExist(err) {
		t.Error("deleted folder dir still present")
	}
	for _, f := range s.Folders() {
		if strings.HasPrefix(f.Name, "Zed") {
			t.Errorf("folder %q still listed", f.Name)
		}
	}
}

// App-owned directory names and Windows device names are rejected as folders.
func TestReservedFolderNamesRejected(t *testing.T) {
	s, _ := newTestStore(t)
	n, _ := s.Create("Pinned")
	for _, bad := range []string{"templates", "backups/x", "attachments", "con", "Work/nul"} {
		if _, err := s.Update(n.ID, UpdateReq{Folder: strp(bad)}); !errors.Is(err, ErrInvalidFolder) {
			t.Errorf("folder %q: got %v, want ErrInvalidFolder", bad, err)
		}
	}
	if _, err := s.CreateFolder("Templates"); err == nil {
		t.Error("CreateFolder(Templates) should fail")
	}
	if err := s.RenameFolder("x", "backups"); !errors.Is(err, ErrInvalidFolder) {
		t.Errorf("rename to reserved: got %v", err)
	}
	// traversal segments are cleaned away, not honored
	if got := cleanFolderPath("../escape"); got != "escape" {
		t.Errorf("cleanFolderPath(../escape) = %q", got)
	}
	if got := cleanFolderPath("a/../../b"); got != "a/b" {
		t.Errorf("cleanFolderPath(a/../../b) = %q", got)
	}
}

// The same title may exist in different folders without a -2 suffix;
// within one folder the suffix still applies.
func TestFilenameUniquenessPerFolder(t *testing.T) {
	s, _ := newTestStore(t)
	n1, _ := s.Create("Same Title")
	n1u := mustUpdate(t, s, n1.ID, UpdateReq{Folder: strp("A")})
	n2, _ := s.Create("Same Title")
	n2u := mustUpdate(t, s, n2.ID, UpdateReq{Folder: strp("B")})
	if path.Base(n1u.file) != path.Base(n2u.file) {
		t.Errorf("basenames differ across folders: %q vs %q", n1u.file, n2u.file)
	}
	n3, _ := s.Create("Same Title")
	n3u := mustUpdate(t, s, n3.ID, UpdateReq{Folder: strp("A")})
	if path.Base(n3u.file) == path.Base(n1u.file) {
		t.Errorf("same folder, same basename: %q vs %q", n3u.file, n1u.file)
	}
	if !strings.HasPrefix(n3u.file, "A/") {
		t.Errorf("n3 not in folder A: %q", n3u.file)
	}
}

// Folder casing snaps to the existing directory instead of forking the tree.
func TestFolderCasingCanonicalized(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.CreateFolder("Work"); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Create("Cased")
	got := mustUpdate(t, s, n.ID, UpdateReq{Folder: strp("work/sub")})
	if got.Folder != "Work/sub" {
		t.Errorf("folder = %q, want Work/sub", got.Folder)
	}
	seen := 0
	for _, f := range s.Folders() {
		if strings.EqualFold(f.Name, "Work") {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("Work listed %d times", seen)
	}
}

// An empty directory dropped into the notes dir shows up as a folder.
func TestEmptyDirIsFolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Loose", "Ends"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Loose": false, "Loose/Ends": false}
	for _, f := range s.Folders() {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("folder %q not listed", name)
		}
	}
}

// Deleting a folder holding a stray non-note file must not delete that file.
func TestDeleteFolderSparesStrayFiles(t *testing.T) {
	s, dir := newTestStore(t)
	if _, err := s.CreateFolder("Mixed"); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "Mixed", "photo.jpg")
	if err := os.WriteFile(stray, []byte("not a note"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder("Mixed"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("stray file was deleted with the folder: %v", err)
	}
}

// Rescan adopts files and folders created, edited, or removed outside the app.
func TestRescanPicksUpExternalChanges(t *testing.T) {
	s, dir := newTestStore(t)
	keep, err := s.Create("Keeper")
	if err != nil {
		t.Fatal(err)
	}

	// externally drop a folder with a plain note in it
	if err := os.MkdirAll(filepath.Join(dir, "dropped"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dropped", "external.md"),
		[]byte("# External\n\nfrom the file explorer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := s.Rescan()
	if res.Added != 1 || res.Changed != 0 || res.Removed != 0 || res.Total != 2 {
		t.Fatalf("after add: %+v", res)
	}
	found := false
	for _, it := range s.List() {
		if it.Title == "External" && it.Folder == "dropped" {
			found = true
		}
	}
	if !found {
		t.Fatalf("external note not adopted: %+v", s.List())
	}
	if hits := s.Search("explorer", false); len(hits) != 1 {
		t.Errorf("search index not rebuilt: %d hits", len(hits))
	}

	// externally rewrite the kept note's body
	base := noteFilename(keep.Created, "Keeper")
	edited := "---\nid: \"" + keep.ID + "\"\ntitle: \"Keeper\"\n---\n\nedited outside\n"
	if err := os.WriteFile(filepath.Join(dir, base+".md"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	res = s.Rescan()
	if res.Added != 0 || res.Changed != 1 || res.Removed != 0 {
		t.Fatalf("after edit: %+v", res)
	}
	n, err := s.Get(keep.ID)
	if err != nil || n.Body != "edited outside\n" {
		t.Fatalf("edited body not picked up: %+v, %v", n, err)
	}

	// externally delete the dropped note
	if err := os.Remove(filepath.Join(dir, "dropped", "external.md")); err != nil {
		t.Fatal(err)
	}
	res = s.Rescan()
	if res.Added != 0 || res.Changed != 0 || res.Removed != 1 || res.Total != 1 {
		t.Fatalf("after delete: %+v", res)
	}
}

// Two hand-made files with the same basename in different folders both load.
func TestDuplicateBasenamesAcrossFolders(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "note.md"),
			[]byte("# In "+sub+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Count() != 2 {
		t.Fatalf("got %d notes, want 2", s.Count())
	}
}
