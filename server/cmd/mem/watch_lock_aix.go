//go:build aix

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockWatchFile(file *os.File) error {
	return unix.FcntlFlock(file.Fd(), unix.F_SETLK, &unix.Flock_t{
		Type: unix.F_WRLCK,
		Len:  1,
	})
}

func unlockWatchFile(file *os.File) error {
	return unix.FcntlFlock(file.Fd(), unix.F_SETLK, &unix.Flock_t{
		Type: unix.F_UNLCK,
		Len:  1,
	})
}
