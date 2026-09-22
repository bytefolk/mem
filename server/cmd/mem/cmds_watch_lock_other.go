//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package main

import (
	"fmt"
	"os"
)

func tryLockWatchFile(_ *os.File) error {
	return fmt.Errorf("watch locks are not supported on this operating system")
}

func isWatchLockBusy(_ error) bool {
	return false
}

func unlockWatchFile(_ *os.File) error {
	return nil
}
