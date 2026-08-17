package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The macOS cache must not sit under the exe (which lives in ~/Documents on a
// normal install) — that's TCC-protected and tectonic panics on it. Everywhere
// else it must stay next to the exe so a USB-stick copy carries its cache.
func TestTectonicCacheDir(t *testing.T) {
	got := tectonicCacheDir()
	portable := filepath.Join(exeDir(), "tools", "tectonic-cache")

	if runtime.GOOS != "darwin" {
		if got != portable {
			t.Fatalf("tectonicCacheDir() = %q, want the portable path %q", got, portable)
		}
		return
	}

	if got == portable {
		t.Fatalf("tectonicCacheDir() returned the portable path %q on darwin — it must move off ~/Documents", got)
	}
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no user cache dir on this machine; the fallback path is the portable one")
	}
	want := filepath.Join(base, "Portanote", "tectonic")
	if got != want {
		t.Fatalf("tectonicCacheDir() = %q, want %q", got, want)
	}
}

// The path is fed to a child process as TECTONIC_CACHE_DIR, so it has to be
// absolute — pandoc runs with cmd.Dir set to a temp folder, not the exe's.
func TestTectonicCacheDirIsAbsolute(t *testing.T) {
	got := tectonicCacheDir()
	if got == "" {
		t.Fatal("tectonicCacheDir() returned an empty path")
	}
	// exeDir() degrades to "." if os.Executable() fails; that's the one
	// documented non-absolute case and it can't happen under `go test`.
	if !filepath.IsAbs(got) && !strings.HasPrefix(got, ".") {
		t.Fatalf("tectonicCacheDir() = %q, want an absolute path", got)
	}
}
