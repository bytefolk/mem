//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockQoderCheckpointFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX)
}

func unlockQoderCheckpointFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
