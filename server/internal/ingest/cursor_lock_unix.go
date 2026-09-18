//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package ingest

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockCursorFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func isCursorLockBusy(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK)
}

func unlockCursorFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
