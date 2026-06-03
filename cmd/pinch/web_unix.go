//go:build !windows

// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// platformPIDAlive returns true when a process with this PID exists.
//
// os.FindProcess on Unix returns a Process for any PID; sending signal 0
// is a portable no-op signal that returns ESRCH for dead PIDs and EPERM
// when the PID exists but is owned by another user. Both ESRCH and "no
// error at all" mean the test we care about (process exists) — only EPERM
// confirms existence; ESRCH disproves it. Treat any non-ESRCH outcome as
// alive so cross-user discovery still works.
func platformPIDAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM = exists, owned by another user — count as alive.
	if errno, ok := err.(syscall.Errno); ok && errno == syscall.EPERM {
		return true
	}
	return false
}

// startDetached spawns cmd so it survives the parent process exiting.
// On Unix this means setsid via SysProcAttr.Setsid so the child becomes
// a session leader with its own process group.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
