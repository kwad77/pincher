//go:build windows

package index

import "golang.org/x/sys/windows"

// processExecutablePath returns the full image path of a running
// process on Windows via QueryFullProcessImageName. An error means the
// process is gone or inaccessible — callers treat that as "identity
// unverifiable" and never terminate on it.
func platformProcessExecutablePath(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}
