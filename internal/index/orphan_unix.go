//go:build !windows

package index

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// processExecutablePath returns the executable of a running process on
// Unix. Linux exposes /proc/<pid>/exe as a symlink; macOS and the BSDs
// have no /proc, so fall back to `ps`. An error means the identity
// can't be verified — callers never terminate on it.
func platformProcessExecutablePath(pid int) (string, error) {
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return exe, nil
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("ps returned no command for pid %d", pid)
	}
	return name, nil
}
