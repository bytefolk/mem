//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || windows)

package ingest

import (
	"fmt"
	"os"
)

func lockCursorFile(_ *os.File) error {
	return fmt.Errorf("cursor locks are not supported on this operating system")
}

func unlockCursorFile(_ *os.File) error {
	return nil
}
