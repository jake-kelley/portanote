//go:build windows

package main

import "syscall"

// detachAttr lets the relaunched process outlive this one, with no shared console.
func detachAttr() *syscall.SysProcAttr {
	const (
		detachedProcess       = 0x00000008
		createNewProcessGroup = 0x00000200
	)
	return &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
}

// noWindowAttr keeps a spawned child (the claude CLI) from flashing a console window.
func noWindowAttr() *syscall.SysProcAttr {
	const createNoWindow = 0x08000000
	return &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
