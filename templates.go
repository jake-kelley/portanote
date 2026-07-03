package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Template struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (s *Store) templatesDir() string { return filepath.Join(s.dir, "templates") }

// Templates lists the .md files in <notes>/templates as reusable note skeletons.
func (s *Store) Templates() []Template {
	entries, err := os.ReadDir(s.templatesDir())
	if err != nil {
		return nil
	}
	out := []Template{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.templatesDir(), e.Name()))
		if err != nil {
			continue
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		n := parseNote(base, string(raw))
		title := n.Title
		if title == base { // no explicit title in the template — leave it blank
			title = ""
		}
		out = append(out, Template{Name: base, Title: title, Body: n.Body})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// seedTemplates drops a few starter templates on first run (the dir is absent).
// They are ordinary editable files — delete or change them freely.
func (s *Store) seedTemplates() {
	dir := s.templatesDir()
	if _, err := os.Stat(dir); err == nil {
		return // already exists — don't clobber the user's templates
	}
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	for name, body := range map[string]string{
		"Meeting Notes": "---\ntitle: \"Meeting — \"\n---\n\n**Date:** \n**Attendees:** \n\n## Agenda\n- \n\n## Notes\n- \n\n## Action items\n- [ ] \n",
		"Runbook":       "---\ntitle: \"Runbook — \"\n---\n\n## Purpose\n\n## Prerequisites\n- \n\n## Steps\n1. \n\n## Rollback\n- \n\n## References\n- \n",
		"Daily Log":     "---\ntitle: \"Daily Log\"\n---\n\n## Today\n- \n\n## Blockers\n- \n\n## Tomorrow\n- \n",
	} {
		_ = os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644)
	}
}
