package main

import (
	"errors"
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

// splitTemplate returns the explicit frontmatter title (if any) and the body.
// Unlike a note, a template's suggested title is never derived from a heading —
// so a template saved from a note (no frontmatter) yields an untitled new note.
func splitTemplate(raw string) (title, body string) {
	body = raw
	if strings.HasPrefix(raw, "---\n") || strings.HasPrefix(raw, "---\r\n") {
		rest := raw[strings.Index(raw, "\n")+1:]
		if end := findFrontmatterEnd(rest); end >= 0 {
			for _, line := range strings.Split(rest[:end], "\n") {
				if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), ":"); ok && strings.TrimSpace(k) == "title" {
					title = unquote(strings.TrimSpace(v))
				}
			}
			body = strings.TrimLeft(strings.TrimPrefix(rest[end:], "---"), "\r\n")
		}
	}
	return title, body
}

// Templates lists the .md files in <notes>/templates as reusable note skeletons.
func (s *Store) Templates() []Template {
	entries, err := os.ReadDir(s.templatesDir())
	if err != nil {
		return []Template{}
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
		title, body := splitTemplate(string(raw))
		out = append(out, Template{Name: base, Title: title, Body: body})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// cleanTemplateName keeps a readable, filesystem-safe template name.
func cleanTemplateName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return -1
		}
		return r
	}, name)
	name = strings.Trim(strings.TrimSpace(name), ".")
	if len(name) > 80 {
		name = strings.TrimSpace(name[:80])
	}
	return name
}

// CreateTemplate saves a note body as a reusable template (overwrites by name).
// The body is stored as-is with no frontmatter, so notes created from it start
// untitled — the template is about the structure, not a fixed title.
func (s *Store) CreateTemplate(name, body string) (string, error) {
	name = cleanTemplateName(name)
	if name == "" {
		return "", errors.New("template name required")
	}
	if err := os.MkdirAll(s.templatesDir(), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(s.templatesDir(), name+".md"), []byte(body), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) DeleteTemplate(name string) error {
	name = cleanTemplateName(name)
	if name == "" {
		return errors.New("template name required")
	}
	err := os.Remove(filepath.Join(s.templatesDir(), name+".md"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
