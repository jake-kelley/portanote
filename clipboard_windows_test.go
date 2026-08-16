//go:build windows

package main

import (
	"encoding/binary"
	"syscall"
	"testing"
)

// TestBuildDropFiles checks the CF_HDROP payload shape without touching any
// Win32 API: the DROPFILES header's pFiles offset, the fWide flag, the
// UTF-16 encoding of the path, and the double-null that terminates the file
// list. This is where the real bugs live — SetClipboardData either accepts
// or silently ignores a malformed payload, so the layout has to be right
// before it ever reaches Win32.
func TestBuildDropFiles(t *testing.T) {
	const path = `C:\Users\jake\Documents\portanote\report.pdf`

	buf, err := buildDropFiles(path)
	if err != nil {
		t.Fatalf("buildDropFiles: %v", err)
	}
	if len(buf) < dropFilesHeaderSize {
		t.Fatalf("payload too short for a DROPFILES header: %d bytes", len(buf))
	}

	pFiles := binary.LittleEndian.Uint32(buf[0:4])
	if pFiles != dropFilesHeaderSize {
		t.Errorf("pFiles = %d, want %d (offset to the file list)", pFiles, dropFilesHeaderSize)
	}

	// pt.x, pt.y, fNC occupy bytes 4:16 and must stay zeroed.
	for i := 4; i < 16; i++ {
		if buf[i] != 0 {
			t.Errorf("byte %d = %d, want 0 (pt/fNC must be zeroed)", i, buf[i])
		}
	}

	fWide := binary.LittleEndian.Uint32(buf[16:20])
	if fWide != 1 {
		t.Errorf("fWide = %d, want 1 (the file list is UTF-16, not ANSI)", fWide)
	}

	wantPath, err := syscall.UTF16FromString(path)
	if err != nil {
		t.Fatalf("UTF16FromString: %v", err)
	}
	list := buf[dropFilesHeaderSize:]
	if len(list) != len(wantPath)*2+2 {
		t.Fatalf("file list is %d bytes, want %d (path + terminating null, doubled)",
			len(list), len(wantPath)*2+2)
	}
	for i, want := range wantPath {
		got := binary.LittleEndian.Uint16(list[i*2 : i*2+2])
		if got != want {
			t.Errorf("utf16[%d] = %#x, want %#x", i, got, want)
		}
	}
	// The last two bytes are the second null that terminates the file
	// list (the first null terminates the single path entry).
	if list[len(list)-2] != 0 || list[len(list)-1] != 0 {
		t.Errorf("file list not double-null terminated: last two bytes = %v", list[len(list)-2:])
	}
}

// TestOpenClipboardWithRetry is not run automatically: it opens the real
// clipboard, which CI does not have, and would fight any interactive
// session that also holds it. Left here only as a manual smoke check.
func TestOpenClipboardWithRetry(t *testing.T) {
	if testing.Short() {
		t.Skip("touches the real OS clipboard; skipped under -short (e.g. in CI)")
	}
	if err := openClipboardWithRetry(); err != nil {
		t.Fatalf("openClipboardWithRetry: %v", err)
	}
	procCloseClipboard.Call()
}
