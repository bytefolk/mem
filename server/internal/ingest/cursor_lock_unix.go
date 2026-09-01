//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ingest

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockCursorFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX)
}

func unlockCursorFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
