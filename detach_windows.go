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
