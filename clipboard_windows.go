//go:build windows

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	// RtlMoveMemory is exported by kernel32.dll (it's the real symbol behind
	// the CopyMemory/MoveMemory macros). Routing the copy through it means the
	// GlobalLock'd destination address never gets converted from uintptr to
	// unsafe.Pointer on the Go side -- it just travels as a syscall argument --
	// which keeps `go vet`'s unsafeptr check happy.
	procMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfHDROP      = 15
	gmemMoveable = 0x0002
	gmemZeroinit = 0x0040
)

// dropFilesHeaderSize is sizeof(DROPFILES): four DWORDs (pFiles, pt.x, pt.y,
// fNC) followed by one BOOL (fWide), all 4 bytes wide on both 32- and 64-bit
// Windows because DROPFILES uses no pointer-sized fields.
const dropFilesHeaderSize = 20

// buildDropFiles builds the CF_HDROP payload: a DROPFILES header followed by
// the file list. Windows expects the list as UTF-16, one path per entry,
// each path null-terminated, with a second null terminating the whole list
// (a double-null after the last path). Kept free of any Win32 calls so it is
// unit-testable.
func buildDropFiles(path string) ([]byte, error) {
	utf16Path, err := syscall.UTF16FromString(path)
	if err != nil {
		return nil, err
	}
	// UTF16FromString already appends one null terminator; add the second
	// to terminate the (single-entry) list.
	buf := make([]byte, dropFilesHeaderSize+len(utf16Path)*2+2)

	// pFiles: offset from the start of this struct to the file list.
	putUint32LE(buf[0:4], dropFilesHeaderSize)
	// pt.x, pt.y, fNC are left zeroed: no drag position, not non-client.
	// fWide = 1: the path list is UTF-16, not ANSI.
	putUint32LE(buf[16:20], 1)

	pathBytes := buf[dropFilesHeaderSize:]
	for i, u := range utf16Path {
		putUint16LE(pathBytes[i*2:i*2+2], u)
	}
	// Trailing double-null is already present because buf was zero-allocated
	// and sized two bytes past the single null UTF16FromString appended.

	return buf, nil
}

func putUint32LE(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func putUint16LE(b []byte, v uint16) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
}

// copyFileToClipboard puts path on the system clipboard as a file reference,
// so pasting into a chat client attaches the file itself.
func copyFileToClipboard(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve absolute path: %w", err)
	}
	abs = filepath.Clean(abs)

	payload, err := buildDropFiles(abs)
	if err != nil {
		return fmt.Errorf("build CF_HDROP payload: %w", err)
	}

	h, _, err := procGlobalAlloc.Call(uintptr(gmemMoveable|gmemZeroinit), uintptr(len(payload)))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: %w", err)
	}

	ptr, _, err := procGlobalLock.Call(h)
	if ptr == 0 {
		// SetClipboardData never ran, so we still own the handle and must
		// free it ourselves or the allocation leaks.
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock: %w", err)
	}
	procMoveMemory.Call(ptr, uintptr(unsafe.Pointer(&payload[0])), uintptr(len(payload)))
	procGlobalUnlock.Call(h)

	if err := openClipboardWithRetry(); err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer procCloseClipboard.Call() // always release, or every other app on the machine stalls

	if ok, _, err := procEmptyClipboard.Call(); ok == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("EmptyClipboard: %w", err)
	}

	if ret, _, err := procSetClipboardData.Call(uintptr(cfHDROP), h); ret == 0 {
		// SetClipboardData failed, so ownership of h never transferred to
		// the system: we still have to free it.
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData: %w", err)
	}
	// From here the clipboard (i.e. the OS) owns h. Calling GlobalFree now
	// would free memory another process may already be reading.

	return nil
}

// openClipboardWithRetry opens the clipboard, retrying briefly because
// OpenClipboard fails whenever another process (commonly an active
// clipboard-history or screenshot tool) is holding it for a moment — that
// contention is normal and transient, not a real error.
func openClipboardWithRetry() error {
	const attempts = 5
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(20 * time.Millisecond * time.Duration(i))
		}
		ok, _, err := procOpenClipboard.Call(0)
		if ok != 0 {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("unknown error")
	}
	return fmt.Errorf("OpenClipboard: %w (clipboard held by another process)", lastErr)
}
