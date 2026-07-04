package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Settings struct {
	BackupIntervalHours int `json:"backupIntervalHours"`
	BackupKeep          int `json:"backupKeep"`
}

func defaultSettings() Settings { return Settings{BackupIntervalHours: 3, BackupKeep: 12} }

func (s *Store) settingsPath() string { return filepath.Join(s.dir, ".portanote-settings.json") }
func (s *Store) backupsDir() string   { return filepath.Join(s.dir, "backups") }

func (s *Store) loadSettings() {
	s.settings = defaultSettings()
	if raw, err := os.ReadFile(s.settingsPath()); err == nil {
		var st Settings
		if json.Unmarshal(raw, &st) == nil {
			if st.BackupIntervalHours > 0 {
				s.settings.BackupIntervalHours = st.BackupIntervalHours
			}
			if st.BackupKeep > 0 {
				s.settings.BackupKeep = st.BackupKeep
			}
		}
	}
}

func (s *Store) GetSettings() Settings {
	s.setMu.Lock()
	defer s.setMu.Unlock()
	return s.settings
}

// SaveSettings merges non-zero fields, persists, and returns the effective values.
func (s *Store) SaveSettings(in Settings) Settings {
	s.setMu.Lock()
	if in.BackupIntervalHours > 0 {
		s.settings.BackupIntervalHours = in.BackupIntervalHours
	}
	if in.BackupKeep > 0 {
		s.settings.BackupKeep = in.BackupKeep
	}
	out := s.settings
	s.setMu.Unlock()
	raw, _ := json.MarshalIndent(out, "", "  ")
	tmp := s.settingsPath() + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		os.Rename(tmp, s.settingsPath())
	}
	return out
}

// Backup zips the whole notes directory (except the backups dir) into
// backups/portanote-<timestamp>.zip, then prunes to the keep count.
func (s *Store) Backup() (string, error) {
	bdir := s.backupsDir()
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return "", err
	}
	name := "portanote-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	f, err := os.Create(filepath.Join(bdir, name))
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// never back up the backups themselves or the git-sync working clones
			if p == bdir || d.Name() == ".git-sync" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, e := filepath.Rel(s.dir, p)
		if e != nil {
			return nil
		}
		zf, e := zw.Create(filepath.ToSlash(rel))
		if e != nil {
			return nil
		}
		src, e := os.Open(p)
		if e != nil {
			return nil
		}
		io.Copy(zf, src)
		src.Close()
		return nil
	})
	zw.Close()
	f.Close()
	s.pruneBackups()
	return name, nil
}

func (s *Store) pruneBackups() {
	keep := s.GetSettings().BackupKeep
	entries, _ := os.ReadDir(s.backupsDir())
	var zips []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "portanote-") && strings.HasSuffix(e.Name(), ".zip") {
			zips = append(zips, e.Name())
		}
	}
	sort.Strings(zips) // timestamped names sort chronologically
	for len(zips) > keep {
		os.Remove(filepath.Join(s.backupsDir(), zips[0]))
		zips = zips[1:]
	}
}

// BackupStatus is surfaced to the settings UI.
type BackupStatus struct {
	Settings
	LastBackup string `json:"lastBackup"` // RFC3339, empty if none
	Count      int    `json:"count"`
}

func (s *Store) BackupStatus() BackupStatus {
	last, count := s.lastBackup()
	st := BackupStatus{Settings: s.GetSettings(), Count: count}
	if !last.IsZero() {
		st.LastBackup = last.UTC().Format(time.RFC3339)
	}
	return st
}

func (s *Store) lastBackup() (time.Time, int) {
	entries, _ := os.ReadDir(s.backupsDir())
	var newest time.Time
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		count++
		if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, count
}

// StartBackups runs the periodic backup loop (checks every minute so interval
// changes take effect promptly; backs up when the interval has elapsed).
func (s *Store) StartBackups() {
	go func() {
		for {
			interval := time.Duration(s.GetSettings().BackupIntervalHours) * time.Hour
			last, _ := s.lastBackup()
			if last.IsZero() || time.Since(last) >= interval {
				s.Backup()
			}
			time.Sleep(time.Minute)
		}
	}()
}
