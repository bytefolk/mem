//go:build aix

package ingest

import (
	"os"

	"golang.org/x/sys/unix"
)

// AIX does not expose flock(2) through x/sys, so use the blocking fcntl record
// lock equivalent for the first byte of the persistent sidecar inode.
func lockCursorFile(file *os.File) error {
	return unix.FcntlFlock(file.Fd(), unix.F_SETLKW, &unix.Flock_t{
		Type: unix.F_WRLCK,
		Len:  1,
	})
}

func unlockCursorFile(file *os.File) error {
	return unix.FcntlFlock(file.Fd(), unix.F_SETLK, &unix.Flock_t{
		Type: unix.F_UNLCK,
		Len:  1,
	})
}
