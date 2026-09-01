//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package main

import (
	"fmt"
	"os"
)

func lockWatchFile(_ *os.File) error {
	return fmt.Errorf("watch locks are not supported on this operating system")
}

func unlockWatchFile(_ *os.File) error {
	return nil
}
