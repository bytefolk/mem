//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package ingest

import (
	"fmt"
	"os"
)

func tryLockCursorFile(_ *os.File) error {
	return fmt.Errorf("cursor locks are not supported on this operating system")
}

func isCursorLockBusy(_ error) bool {
	return false
}

func unlockCursorFile(_ *os.File) error {
	return nil
}
