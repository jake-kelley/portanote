//go:build !windows

package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// copyFileToClipboard puts path on the system clipboard as a file reference,
// so pasting into a chat client attaches the file itself.
//
// On macOS this shells out to osascript to set the Finder clipboard to a
// POSIX file, which is what Cmd+C on a file in Finder does. Whether Teams
// (or Slack, or any other app) on macOS actually accepts that as a
// paste-to-attach is unverified — this is best effort, not a guarantee.
// Every other platform (Linux, BSD, ...) has no equivalent OS-level concept
// of "file on the clipboard" that this process can drive without a desktop
// clipboard manager and format the caller would need to know about, so it
// returns an error and lets the caller fall back to a normal download.
func copyFileToClipboard(path string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("clipboard file copy is not supported on %s", runtime.GOOS)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}
	abs = filepath.Clean(abs)

	// AppleScript string literal: escape backslashes and quotes so the path
	// can't break out of the "..." it's embedded in.
	escaped := strings.ReplaceAll(strings.ReplaceAll(abs, `\`, `\\`), `"`, `\"`)
	script := fmt.Sprintf(`set the clipboard to POSIX file "%s"`, escaped)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel() // a hung osascript must not wedge the request

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("osascript: %w: %s", err, out)
	}
	return nil
}
