package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// buildUpload assembles the multipart body the Import modal sends: files under
// "files[]" with their drop-relative path as the filename, plus the optional
// code and mtimes fields.
func buildUpload(t *testing.T, files map[string][]byte, fields map[string]string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	// paths are collected in the same pass, so they stay aligned with the parts
	// however the map happens to iterate
	var paths []string
	for name, data := range files {
		p, err := w.CreateFormFile("files[]", name)
		if err != nil {
			t.Fatal(err)
		}
		p.Write(data)
		paths = append(paths, name)
	}
	if len(paths) > 0 {
		raw, _ := json.Marshal(paths)
		w.WriteField("paths", string(raw))
	}
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	return w.FormDataContentType(), buf.Bytes()
}

func postUpload(t *testing.T, store *Store, path, ct string, body []byte) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	registerShareRoutes(mux, store)
	req := httptest.NewRequest("POST", path, bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s returned %d: %s", path, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding %s: %v (%s)", path, err, rec.Body.String())
	}
	return out
}

// A foreign note references its images by a path relative to its own file. When
// the whole folder is dropped, those have to resolve against the other files in
// the drop — this is the case that makes "import my Obsidian vault" work rather
// than producing a note full of dead images.
func TestImportResolvesRelativeImagesInADroppedFolder(t *testing.T) {
	s, _ := newTestStore(t)
	md := "# Alerting overview\n\n![architecture](images/diagram.png)\n"
	ct, body := buildUpload(t, map[string][]byte{
		"vault/Alerting overview.md": []byte(md),
		"vault/images/diagram.png":   tinyPNG,
	}, nil)

	got := postUpload(t, s, "/api/import/preview", ct, body)
	items := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("preview returned %d items, want 1", len(items))
	}
	item := items[0].(map[string]any)
	if n := item["attachments"].(float64); n != 1 {
		t.Errorf("attachments = %v, want 1 — the relative image did not resolve", n)
	}
	if ws, ok := item["warnings"].([]any); ok && len(ws) > 0 {
		t.Errorf("unexpected warnings: %v", ws)
	}
}

// The same note dropped on its own cannot resolve the image. That has to be
// said out loud in the preview rather than silently importing a broken link.
func TestImportWarnsWhenADroppedImageIsMissing(t *testing.T) {
	s, _ := newTestStore(t)
	md := "# Alerting overview\n\n![architecture](images/diagram.png)\n"
	ct, body := buildUpload(t, map[string][]byte{"vault/Alerting overview.md": []byte(md)}, nil)

	item := postUpload(t, s, "/api/import/preview", ct, body)["items"].([]any)[0].(map[string]any)
	ws, _ := item["warnings"].([]any)
	if len(ws) == 0 || !strings.Contains(ws[0].(string), "images/diagram.png") {
		t.Fatalf("warnings = %v, want one naming the unresolved image", ws)
	}
	if n := item["attachments"].(float64); n != 0 {
		t.Errorf("attachments = %v, want 0", n)
	}
}

// A multipart part carries no modification time, so the UI sends them
// alongside. Without that a foreign file imports as created today, discarding
// the only date it had.
func TestImportUsesSuppliedModificationTime(t *testing.T) {
	s, _ := newTestStore(t)
	want := time.Date(2025, 11, 4, 8, 30, 0, 0, time.UTC)
	ct, body := buildUpload(t,
		map[string][]byte{"bare.md": []byte("# Node drain checklist\n\nno frontmatter here\n")},
		map[string]string{
			"mtimes": fmt.Sprintf(`{"bare.md":%d}`, want.UnixMilli()),
			"folder": "Inbox",
		})

	res := postUpload(t, s, "/api/import", ct, body)
	if res["imported"].(float64) != 1 {
		t.Fatalf("imported = %v, want 1 (%v)", res["imported"], res["warnings"])
	}
	id := res["ids"].([]any)[0].(string)
	n, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !n.Created.Equal(want) {
		t.Errorf("created = %s, want %s", n.Created, want)
	}
	if n.Title != "Node drain checklist" {
		t.Errorf("title = %q, want it derived from the H1", n.Title)
	}
	// the zero-time trap: a year-0001 created date names the file 01JANUARY0001
	if strings.HasPrefix(n.file, "0001") || strings.Contains(n.file, "01JANUARY0001") {
		t.Errorf("file = %q, written from a zero timestamp", n.file)
	}
}

// The whole point of the feature: what one machine shares, another imports.
func TestShareCodeRoundTripsThroughTheAPI(t *testing.T) {
	s, _ := newTestStore(t)
	src, err := s.Create("Quarantine runbook")
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, s, src.ID, UpdateReq{
		Body: strp("step one\nstep two\n"),
		Tags: &[]string{"aws", "security"},
	})
	if src, err = s.Get(src.ID); err != nil { // Create returned the pre-update copy
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerShareRoutes(mux, s)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/share/"+src.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("share returned %d: %s", rec.Code, rec.Body.String())
	}
	var share map[string]any
	json.Unmarshal(rec.Body.Bytes(), &share)
	if share["mode"] != "code" {
		t.Fatalf("mode = %v, want code for a note with no attachments", share["mode"])
	}

	ct, body := buildUpload(t, nil, map[string]string{
		"code":   share["code"].(string),
		"folder": "Inbox",
	})
	res := postUpload(t, s, "/api/import", ct, body)
	if res["imported"].(float64) != 1 {
		t.Fatalf("imported = %v, want 1 (%v)", res["imported"], res["warnings"])
	}

	got, err := s.Get(res["ids"].([]any)[0].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == src.ID {
		t.Error("imported note reused the sender's id")
	}
	if got.Title != src.Title || got.Body != src.Body {
		t.Errorf("round trip changed the note: %q / %q", got.Title, got.Body)
	}
	if got.Folder != "Inbox" {
		t.Errorf("folder = %q, want the chosen destination", got.Folder)
	}
	if !got.Created.Equal(src.Created) {
		t.Errorf("created = %s, want the sender's %s", got.Created, src.Created)
	}
}

// A share code that survived a chat client's line wrapping still has to import.
func TestImportAcceptsAWrappedShareCode(t *testing.T) {
	s, _ := newTestStore(t)
	src, err := s.Create("Wrapped in transit")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildBundle(s, []string{src.ID})
	if err != nil {
		t.Fatal(err)
	}
	code := encodeShareCode(b.Zip)
	var wrapped strings.Builder
	for i, r := range code {
		if i > 0 && i%72 == 0 {
			wrapped.WriteString("\r\n")
		}
		wrapped.WriteRune(r)
	}

	ct, body := buildUpload(t, nil, map[string]string{"code": wrapped.String(), "folder": ""})
	res := postUpload(t, s, "/api/import", ct, body)
	if res["imported"].(float64) != 1 {
		t.Fatalf("imported = %v, want 1 (%v)", res["imported"], res["warnings"])
	}
}
