package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var attachRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// image mime -> file extension (svg intentionally excluded — script vector)
var imgExt = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp",
}

func newAPI(store *Store, uiFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		pandoc, engine := pandocPath(), pdfEnginePath()
		writeJSON(w, http.StatusOK, map[string]any{
			"version": version,
			"dir":     store.dir,
			"pandoc":  pandoc,
			"engine":  engine,
			"eisvogel": pandoc != "" && engine != "",
		})
	})

	mux.HandleFunc("GET /api/notes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.List())
	})

	mux.HandleFunc("POST /api/notes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title string `json:"title"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		n, err := store.Create(req.Title)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, n)
	})

	mux.HandleFunc("GET /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		n, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, n)
	})

	mux.HandleFunc("PUT /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req UpdateReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		n, err := store.Update(r.PathValue("id"), req)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, n)
	})

	mux.HandleFunc("DELETE /api/notes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		n, err := store.Get(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		if !n.Trashed {
			writeErr(w, http.StatusConflict, errors.New("note must be trashed before permanent deletion"))
			return
		}
		if err := store.Delete(id); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/templates", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.Templates())
	})

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.BackupStatus())
	})

	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in Settings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		store.SaveSettings(in)
		writeJSON(w, http.StatusOK, store.BackupStatus())
	})

	mux.HandleFunc("POST /api/backup", func(w http.ResponseWriter, r *http.Request) {
		name, err := store.Backup()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": name})
	})

	// ---- Git sync (manual pull / commit+push; per-folder; branch-selectable) ----
	mux.HandleFunc("GET /api/sync", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.SyncStatus())
	})
	mux.HandleFunc("PUT /api/sync/auth", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Token string }
		json.NewDecoder(r.Body).Decode(&in)
		store.SetGitAuth(in.Username, in.Token)
		writeJSON(w, http.StatusOK, store.SyncStatus())
	})
	mux.HandleFunc("PUT /api/sync/folder", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path, RemoteURL, Branch string }
		json.NewDecoder(r.Body).Decode(&in)
		if err := store.ConfigureFolderSync(in.Path, in.RemoteURL, in.Branch); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, store.SyncStatus())
	})
	mux.HandleFunc("DELETE /api/sync/folder", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		json.NewDecoder(r.Body).Decode(&in)
		store.UnlinkFolderSync(in.Path)
		writeJSON(w, http.StatusOK, store.SyncStatus())
	})
	mux.HandleFunc("PUT /api/sync/branch", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path, Branch string }
		json.NewDecoder(r.Body).Decode(&in)
		store.SetSyncBranch(in.Path, in.Branch)
		writeJSON(w, http.StatusOK, store.SyncStatus())
	})
	mux.HandleFunc("GET /api/sync/branches", func(w http.ResponseWriter, r *http.Request) {
		branches, err := store.SyncBranches(r.URL.Query().Get("path"))
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]string{"branches": branches})
	})
	mux.HandleFunc("POST /api/sync/pull", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path string }
		json.NewDecoder(r.Body).Decode(&in)
		log, err := store.SyncPull(in.Path)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "log": log})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"log": log})
	})
	mux.HandleFunc("POST /api/sync/push", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Path, Message string }
		json.NewDecoder(r.Body).Decode(&in)
		log, err := store.SyncPush(in.Path, in.Message)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "log": log})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"log": log})
	})

	mux.HandleFunc("GET /api/folders", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.Folders())
	})

	mux.HandleFunc("POST /api/folders", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		name, err := store.CreateFolder(req.Name)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": name})
	})

	mux.HandleFunc("POST /api/folders/rename", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if err := store.RenameFolder(req.From, req.To); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, store.Folders())
	})

	mux.HandleFunc("POST /api/folders/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if err := store.DeleteFolder(req.Name); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, store.Folders())
	})

	mux.HandleFunc("GET /api/notes/{id}/suggest-tags", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string][]string{"tags": store.SuggestTags(r.PathValue("id"), 6)})
	})

	mux.HandleFunc("GET /api/notes/{id}/backlinks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.Backlinks(r.PathValue("id")))
	})

	// image attachments (pasted screenshots etc.), stored in <notes>/attachments
	attachDir := filepath.Join(store.dir, "attachments")
	mux.HandleFunc("POST /api/attachments", func(w http.ResponseWriter, r *http.Request) {
		ext := imgExt[strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))]
		if ext == "" {
			writeErr(w, http.StatusUnsupportedMediaType, errors.New("only png, jpeg, gif, webp images are accepted"))
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 25<<20)) // 25 MB cap
		if err != nil || len(data) == 0 {
			writeErr(w, http.StatusBadRequest, errors.New("empty or unreadable upload"))
			return
		}
		if err := os.MkdirAll(attachDir, 0o755); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		b := make([]byte, 4)
		rand.Read(b)
		name := time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b) + "." + ext
		if err := os.WriteFile(filepath.Join(attachDir, name), data, 0o644); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"path": "attachments/" + name})
	})

	mux.HandleFunc("GET /attachments/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !attachRe.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(attachDir, name))
	})

	mux.HandleFunc("GET /api/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		includeTrashed := r.URL.Query().Get("trash") == "1"
		writeJSON(w, http.StatusOK, store.Search(q, includeTrashed))
	})

	mux.HandleFunc("GET /api/export/pdf/{id}", func(w http.ResponseWriter, r *http.Request) {
		n, err := store.Get(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		opts := ExportOpts{
			TOC:       r.URL.Query().Get("toc") == "1",
			TitlePage: r.URL.Query().Get("titlepage") == "1",
			NotesDir:  store.dir,
		}
		pdf, err := ExportEisvogelPDF(n, opts)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeFilename(n.Title)+`.pdf"`)
		w.Write(pdf)
	})

	// print-to-PDF view (the zero-dependency export path)
	mux.HandleFunc("GET /print/{id}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, uiFS, "print.html")
	})

	mux.Handle("/", http.FileServerFS(uiFS))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

var unsafeFile = regexp.MustCompile(`[^A-Za-z0-9 ._-]+`)

func safeFilename(s string) string {
	s = unsafeFile.ReplaceAllString(s, "_")
	s = strings.Trim(s, " ._")
	if s == "" {
		s = "note"
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}
