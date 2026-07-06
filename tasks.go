package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A Task is a standalone to-do item — independent of notes. It may optionally
// link back to the note it was created from (NoteID). Slice order is the
// display order (drag-to-reorder rewrites the slice).
type Task struct {
	ID      string    `json:"id"`
	Text    string    `json:"text"`
	Done    bool      `json:"done"`
	NoteID  string    `json:"noteId,omitempty"` // note this task was created from
	Created time.Time `json:"created"`
}

func (s *Store) tasksPath() string { return filepath.Join(s.dir, ".portanote-tasks.json") }

func (s *Store) loadTasks() {
	s.tasks = []*Task{}
	if raw, err := os.ReadFile(s.tasksPath()); err == nil {
		var m struct {
			Tasks []*Task `json:"tasks"`
		}
		if json.Unmarshal(raw, &m) == nil && m.Tasks != nil {
			s.tasks = m.Tasks
		}
	}
}

func (s *Store) saveTasksLocked() {
	raw, _ := json.MarshalIndent(map[string][]*Task{"tasks": s.tasks}, "", "  ")
	tmp := s.tasksPath() + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, s.tasksPath())
	}
}

func (s *Store) Tasks() []*Task {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	out := make([]*Task, len(s.tasks))
	copy(out, s.tasks)
	return out
}

func newTaskID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "t-" + hex.EncodeToString(b)
}

func (s *Store) CreateTask(text, noteID string) (*Task, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("task text required")
	}
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	t := &Task{ID: newTaskID(), Text: text, NoteID: noteID, Created: time.Now().UTC()}
	s.tasks = append(s.tasks, t)
	s.saveTasksLocked()
	return t, nil
}

func (s *Store) UpdateTask(id string, done *bool, text *string) (*Task, error) {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	for _, t := range s.tasks {
		if t.ID == id {
			if done != nil {
				t.Done = *done
			}
			if text != nil {
				t.Text = strings.TrimSpace(*text)
			}
			s.saveTasksLocked()
			cp := *t
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) DeleteTask(id string) {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	out := s.tasks[:0:0]
	for _, t := range s.tasks {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.tasks = out
	s.saveTasksLocked()
}

// ClearDoneTasks removes every completed task ("clear history").
func (s *Store) ClearDoneTasks() {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	out := []*Task{}
	for _, t := range s.tasks {
		if !t.Done {
			out = append(out, t)
		}
	}
	s.tasks = out
	s.saveTasksLocked()
}

// ReorderTasks rewrites the task order to match ids; any task not listed is
// appended at the end (keeps unknown/omitted tasks safe).
func (s *Store) ReorderTasks(ids []string) {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	byID := map[string]*Task{}
	for _, t := range s.tasks {
		byID[t.ID] = t
	}
	out := make([]*Task, 0, len(s.tasks))
	seen := map[string]bool{}
	for _, id := range ids {
		if t, ok := byID[id]; ok && !seen[id] {
			out = append(out, t)
			seen[id] = true
		}
	}
	for _, t := range s.tasks {
		if !seen[t.ID] {
			out = append(out, t)
		}
	}
	s.tasks = out
	s.saveTasksLocked()
}
