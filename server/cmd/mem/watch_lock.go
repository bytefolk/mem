package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type watchLock struct {
	file *os.File
}

func acquireWatchLock(lockPath string) (*watchLock, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lockWatchFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire watch lock: %w", err)
	}
	return &watchLock{file: file}, nil
}

func (l *watchLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockWatchFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("unlock watch lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close watch lock file: %w", closeErr)
	}
	return nil
}
