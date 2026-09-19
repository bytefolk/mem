package ingest

import (
	"fmt"
	"os"
	"time"
)

const (
	cursorLockWait  = 5 * time.Second
	cursorLockRetry = 10 * time.Millisecond
)

// cursorLock holds an advisory lock on one cursor sidecar. The sidecar
// deliberately remains on disk after release: unlinking a locked file can
// create a second inode that another process locks independently. The OS
// releases the advisory lock when this descriptor, or its owning process, exits.
type cursorLock struct {
	file *os.File
}

func acquireCursorLock(cursorPath string) (*cursorLock, error) {
	return acquireCursorLockWithTimeout(cursorPath, cursorLockWait)
}

func acquireCursorLockWithTimeout(cursorPath string, timeout time.Duration) (*cursorLock, error) {
	file, err := os.OpenFile(cursorPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := tryLockCursorFile(file); err == nil {
			return &cursorLock{file: file}, nil
		} else if !isCursorLockBusy(err) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire OS lock: %w", err)
		} else if !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("acquire OS lock: timed out after %s: %w", timeout, err)
		}
		time.Sleep(cursorLockRetry)
	}
}

func (l *cursorLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockCursorFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("unlock OS lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close lock file: %w", closeErr)
	}
	return nil
}
