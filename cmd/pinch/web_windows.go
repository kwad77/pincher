//go:build windows

// SPDX-License-Identifier: MIT

package main

import (
	"os/exec"
	"syscall"
)

// platformPIDAlive returns true when a process with this PID exists.
//
// On Windows os.FindProcess succeeds for any PID; we have to actually open
// a handle to confirm. syscall.OpenProcess with PROCESS_QUERY_LIMITED_INFORMATION
// returns ERROR_INVALID_PARAMETER for dead PIDs and a usable handle for live
// ones (and ERROR_ACCESS_DENIED for live PIDs we don't have rights to —
// counted as alive since something is there).
func platformPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
	h, err := syscall.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		syscall.CloseHandle(h)
		return true
	}
	if errno, ok := err.(syscall.Errno); ok {
		// ERROR_ACCESS_DENIED (5) — process exists, but we can't open it.
		if errno == 5 {
			return true
		}
	}
	return false
}

// startDetached spawns cmd so it survives the parent process exiting.
//
// On Windows DETACHED_PROCESS removes the inherited console; CREATE_NEW_PROCESS_GROUP
// puts the child in its own process group so Ctrl-C in the parent's
// console doesn't propagate. CREATE_NO_WINDOW is added defensively so a
// background pincher doesn't pop a black console window.
func startDetached(cmd *exec.Cmd) error {
	const (
		DETACHED_PROCESS         = 0x00000008
		CREATE_NEW_PROCESS_GROUP = 0x00000200
		CREATE_NO_WINDOW         = 0x08000000
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	return cmd.Start()
}
