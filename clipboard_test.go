package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCopyFileToClipboardUnsupportedPlatform exercises the non-Windows,
// non-macOS path (Linux, BSD, ...): it must return a plain error, not
// panic, so the caller can fall back to a normal download.
func TestCopyFileToClipboardUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("only exercises the unsupported-platform fallback")
	}
	if err := copyFileToClipboard("somefile.txt"); err == nil {
		t.Fatal("expected an error on an unsupported platform, got nil")
	}
}

// TestCopyFileToClipboardLive actually touches the OS clipboard. CI has no
// interactive session and no clipboard, so this only runs outside -short
// mode, on the platforms that implement it.
func TestCopyFileToClipboardLive(t *testing.T) {
	if testing.Short() {
		t.Skip("touches the real OS clipboard; skipped under -short (e.g. in CI)")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("clipboard file copy is not implemented on this platform")
	}

	dir := t.TempDir()
	name := filepath.Join(dir, "clipboard-test.txt")
	if err := os.WriteFile(name, []byte("clipboard test"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	if err := copyFileToClipboard(name); err != nil {
		t.Fatalf("copyFileToClipboard: %v", err)
	}
}
