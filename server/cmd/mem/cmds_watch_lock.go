package main

import (
	"fmt"
	"os"
	"path/filepath"
)

type watchLock struct {
	file *os.File
}

func acquireWatchLock(path string) (*watchLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create watch lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open watch lock: %w", err)
	}
	if err := tryLockWatchFile(file); err != nil {
		_ = file.Close()
		if isWatchLockBusy(err) {
			return nil, errWatchLocked
		}
		return nil, fmt.Errorf("acquire watch lock: %w", err)
	}
	return &watchLock{file: file}, nil
}

func (l *watchLock) release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlockWatchFile(l.file)
	_ = l.file.Close()
	l.file = nil
}
