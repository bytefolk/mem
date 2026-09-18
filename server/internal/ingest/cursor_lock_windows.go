//go:build windows

package ingest

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Lock a one-byte range. Windows releases a LockFileEx lock when the owning
// process or file handle exits, matching the Unix advisory-lock lifecycle.
func tryLockCursorFile(file *os.File) error {
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&windows.Overlapped{},
	)
}

func isCursorLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func unlockCursorFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
