//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockWatchFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func isWatchLockBusy(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK)
}

func unlockWatchFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
