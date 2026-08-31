package main

import (
	"fmt"
	"os"
)

// qoderCheckpointLock holds an advisory lock on one cursor sidecar. The
// sidecar deliberately remains on disk after release: unlinking a locked file
// can create a second inode that another process locks independently. The OS
// releases the advisory lock when this descriptor, or its owning process,
// exits.
type qoderCheckpointLock struct {
	file *os.File
}

func acquireQoderCheckpointLock(checkpointPath string) (*qoderCheckpointLock, error) {
	file, err := os.OpenFile(checkpointPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := lockQoderCheckpointFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire OS lock: %w", err)
	}
	return &qoderCheckpointLock{file: file}, nil
}

func (l *qoderCheckpointLock) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockQoderCheckpointFile(l.file)
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
