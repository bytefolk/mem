//go:build windows

package ingest

import (
	"os"

	"golang.org/x/sys/windows"
)

// Lock a one-byte range. Windows releases a LockFileEx lock when the owning
// process or file handle exits, matching the Unix advisory-lock lifecycle.
func lockCursorFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
}

func unlockCursorFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
