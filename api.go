package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

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
