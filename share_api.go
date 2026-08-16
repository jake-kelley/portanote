package main

// HTTP routes for sharing and importing notes. The share side picks its own
// transport by size; the import side accepts every shape a note can arrive in —
// a .portanote bundle, a pasted code, Portanote's own markdown, foreign
// markdown, or a whole dropped folder — and funnels them into one preview.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The upload ceiling sits a little above the bundle limit so an oversized
// bundle is rejected by ReadBundle with a message about bundles, rather than
// by the transport with a message about bytes.
const maxUploadBytes = maxBundleBytes + 16<<20

// mdImageRe finds the target of a markdown image. Foreign notes reference
// images by a path relative to their own file, so unlike attachRefRe (which
// only has to recognize Portanote's own minted names) this has to catch
// anything an editor might have written.
var mdImageRe = regexp.MustCompile(`!\[[^\]]*\]\(([^)\s]+)`)

func registerShareRoutes(mux *http.ServeMux, store *Store) {
	mux.HandleFunc("POST /api/share/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, err := BuildBundle(store, []string{r.PathValue("id")})
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		resp := map[string]any{
			"filename":    b.Filename,
			"notes":       b.Notes,
			"attachments": b.Attachments,
			"bytes":       len(b.Zip),
			"clipboard":   false,
		}
		if b.UseCode {
			resp["mode"] = "code"
			resp["code"] = encodeShareCode(b.Zip)
			writeJSON(w, http.StatusOK, resp)
			return
		}
		// Too big to paste as text, so it goes out as a file — and the file is
		// put on the OS clipboard, which is the only way a chat client can be
		// handed an attachment by a paste. The browser cannot do this itself.
		resp["mode"] = "file"
		if p, err := stageShareFile(b); err == nil {
			resp["clipboard"] = copyFileToClipboard(p) == nil
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("GET /api/share/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		b, err := BuildBundle(store, []string{r.PathValue("id")})
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+b.Filename+`"`)
		w.Write(b.Zip)
	})

	mux.HandleFunc("POST /api/import/preview", func(w http.ResponseWriter, r *http.Request) {
		items, err := collectIncoming(w, r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		out := make([]map[string]any, 0, len(items))
		for i := range items {
			n := items[i].Note
			bytes := len(n.Body)
			for _, d := range items[i].Attachments {
				bytes += len(d)
			}
			out = append(out, map[string]any{
				"title":       n.Title,
				"folder":      items[i].Folder,
				"tags":        n.Tags,
				"attachments": len(items[i].Attachments),
				"bytes":       bytes,
				"warnings":    items[i].Warnings,
				"titleClash":  store.hasTitle(n.Title),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out, "warnings": []string{}})
	})

	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		items, err := collectIncoming(w, r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if len(items) == 0 {
			writeErr(w, http.StatusBadRequest, errors.New("nothing to import"))
			return
		}
		RebaseFolders(items, cleanFolderPath(r.FormValue("folder")))

		ids := make([]string, 0, len(items))
		warnings := []string{}
		for i := range items {
			warnings = append(warnings, items[i].Warnings...)
			n, err := store.Import(&items[i], items[i].Folder)
			if err != nil {
				// One bad note shouldn't discard the rest of a folder import;
				// say which one failed and keep going.
				warnings = append(warnings, fmt.Sprintf("%q was not imported: %v", items[i].Note.Title, err))
				continue
			}
			ids = append(ids, n.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"imported": len(ids), "ids": ids, "warnings": warnings})
	})
}

// stageShareFile writes the bundle somewhere the OS clipboard can point at.
// Temp rather than a folder in the notes directory: a "shared" folder there
// would have to become a reserved name, and forbidding the operator from ever
// naming a folder that is a poor trade for a file they only need for a moment.
func stageShareFile(b *BuiltBundle) (string, error) {
	dir := filepath.Join(os.TempDir(), "portanote-share")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// yesterday's shares are litter; the clipboard only ever holds the newest
	if entries, err := os.ReadDir(dir); err == nil {
		cutoff := time.Now().Add(-24 * time.Hour)
		for _, e := range entries {
			if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	p := filepath.Join(dir, b.Filename)
	if err := os.WriteFile(p, b.Zip, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// collectIncoming turns one multipart upload into notes ready for preview or
// import. The same body serves both endpoints, so the preview a person approves
// is produced by exactly the code that then does the work.
func collectIncoming(w http.ResponseWriter, r *http.Request) ([]Incoming, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		return nil, fmt.Errorf("could not read the upload: %w", err)
	}

	var items []Incoming

	if code := strings.TrimSpace(r.FormValue("code")); code != "" {
		zipBytes, err := decodeShareCode(code)
		if err != nil {
			return nil, err
		}
		got, err := ReadBundle(zipBytes)
		if err != nil {
			return nil, err
		}
		items = append(items, got...)
	}

	// The browser cannot put a file's modification time in a multipart part, so
	// the UI sends them alongside. Without this every foreign markdown file
	// would import as created today, discarding the one date it did carry.
	mtimes := map[string]int64{}
	if raw := r.FormValue("mtimes"); raw != "" {
		json.Unmarshal([]byte(raw), &mtimes)
	}

	// RFC 7578 says a multipart filename must not carry directory information,
	// and Go's reader enforces it with filepath.Base — so a dropped folder's
	// shape cannot travel in the parts themselves. The UI sends the relative
	// paths in their own field, in the same order as the files.
	var paths []string
	if raw := r.FormValue("paths"); raw != "" {
		json.Unmarshal([]byte(raw), &paths)
	}

	var (
		markdown []*multipartFile
		images   = map[string][]byte{}
	)
	if r.MultipartForm != nil {
		for i, fh := range r.MultipartForm.File["files[]"] {
			data, err := readMultipart(fh.Filename, fh)
			if err != nil {
				return nil, err
			}
			raw := fh.Filename
			if i < len(paths) && paths[i] != "" {
				raw = paths[i]
			}
			name := path.Clean(filepath.ToSlash(raw))
			switch {
			case strings.HasSuffix(strings.ToLower(name), bundleExt):
				got, err := ReadBundle(data)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path.Base(name), err)
				}
				items = append(items, got...)
			case isMarkdownName(name):
				markdown = append(markdown, &multipartFile{name: name, data: data})
			default:
				if _, ok := sniffImageExt(data); ok {
					images[name] = data
				}
			}
		}
	}

	for _, md := range markdown {
		mt := time.Now().UTC()
		if ms, ok := mtimes[md.name]; ok && ms > 0 {
			mt = time.UnixMilli(ms).UTC()
		}
		in, err := ParseMarkdownImport(md.name, md.data, mt)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path.Base(md.name), err)
		}
		attachDroppedImages(in, md.name, images)
		items = append(items, *in)
	}
	return items, nil
}

type multipartFile struct {
	name string
	data []byte
}

func readMultipart(name string, fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path.Base(name), err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxEntryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path.Base(name), err)
	}
	if len(data) > maxEntryBytes {
		return nil, fmt.Errorf("%s is larger than the %d MB limit", path.Base(name), maxEntryBytes>>20)
	}
	return data, nil
}

// attachDroppedImages resolves the images a foreign note references against the
// other files dropped with it. The reference is kept verbatim as the map key
// because Store.Import rewrites by exact string match, which means a Hugo note
// saying images/diagram.png works without the importer knowing anything about
// Hugo's layout.
func attachDroppedImages(in *Incoming, mdName string, images map[string][]byte) {
	base := path.Dir(mdName)
	for _, m := range mdImageRe.FindAllStringSubmatch(in.Note.Body, -1) {
		ref := m[1]
		if strings.HasPrefix(ref, bundleAttachDir) || strings.Contains(ref, "://") {
			continue // already ours, or remote and none of our business
		}
		if in.Attachments == nil {
			in.Attachments = map[string][]byte{}
		}
		if _, done := in.Attachments[ref]; done {
			continue
		}
		if data, ok := images[path.Clean(path.Join(base, ref))]; ok {
			in.Attachments[ref] = data
			continue
		}
		in.Warnings = append(in.Warnings, "image not found in the drop: "+ref)
	}
}

func isMarkdownName(name string) bool {
	l := strings.ToLower(name)
	return strings.HasSuffix(l, ".md") || strings.HasSuffix(l, ".markdown") || strings.HasSuffix(l, ".mdown")
}

// hasTitle reports whether a note by this title already exists anywhere. The
// preview uses it to warn about a duplicate before the import happens —
// nothing is overwritten either way, but two notes with one name is a surprise
// worth having in advance.
func (s *Store) hasTitle(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, n := range s.notes {
		if !n.Trashed && strings.ToLower(strings.TrimSpace(n.Title)) == t {
			return true
		}
	}
	return false
}
