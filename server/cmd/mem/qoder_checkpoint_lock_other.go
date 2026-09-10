//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package main

import (
	"fmt"
	"os"
)

func lockQoderCheckpointFile(_ *os.File) error {
	return fmt.Errorf("checkpoint locks are not supported on this operating system")
}

func unlockQoderCheckpointFile(_ *os.File) error {
	return nil
}
