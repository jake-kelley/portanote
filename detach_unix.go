//go:build !windows

package main

import "syscall"

// detachAttr puts the relaunched process in its own session so it survives
// this process exiting.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
