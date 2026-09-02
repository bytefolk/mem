package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// qoderCheckpoint is the persisted per-transcript cursor that makes `mem ingest
// qoder` incremental: it records how many leading lines were already ingested so
// a re-run processes only newly appended messages. Combined with the stable
// Idempotency-Key per line, re-runs are both fast and idempotent.
type qoderCheckpoint struct {
	Abs      string `json:"abs"`       // absolute path of the transcript
	Size     int64  `json:"size"`      // file size at write time (diagnostic)
	ModTime  string `json:"mtime"`     // file mtime at write time (diagnostic)
	LastLine int    `json:"last_line"` // highest 1-based line already ingested
}

// qoderCheckpointPath returns the state-dir-relative path for a transcript's
// cursor, keyed by a content-stable hash of its absolute path.
func qoderCheckpointPath(stateDir, abs string) string {
	sum := sha1.Sum([]byte(abs))
	return filepath.Join(stateDir, hex.EncodeToString(sum[:])+".json")
}

// loadQoderCheckpoint reads a transcript cursor. A missing or malformed cursor
// yields the zero value (LastLine 0), meaning "nothing ingested yet" — never a
// hard error, so a corrupt cursor cannot block ingest.
//
// If the on-disk file is now smaller than when the checkpoint was written, the
// file was truncated and rewritten — reset LastLine so re-ingestion does not
// skip the new content at formerly-ingested line numbers.
func loadQoderCheckpoint(stateDir, abs string) qoderCheckpoint {
	var cp qoderCheckpoint
	p := qoderCheckpointPath(stateDir, abs)
	b, err := os.ReadFile(p)
	if err != nil {
		return cp
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		return qoderCheckpoint{Abs: abs}
	}
	if cp.Abs == "" {
		cp.Abs = abs
	}
	// Detect truncation: if the file was rewritten and is now smaller, reset
	// the cursor so the new content at formerly-ingested line numbers is not
	// silently skipped.
	if cp.Size > 0 {
		if fi, err := os.Stat(abs); err == nil && fi.Size() < cp.Size {
			cp.LastLine = 0
		}
	}
	return cp
}

// saveQoderCheckpoint atomically persists a transcript cursor. The per-cursor
// OS-backed lock covers the read/merge/write sequence so independent ingest
// processes cannot move LastLine backwards or share a staging path. Errors are
// returned (callers may warn without failing the whole ingest).
func saveQoderCheckpoint(stateDir string, cp qoderCheckpoint) (err error) {
	p := qoderCheckpointPath(stateDir, cp.Abs)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create checkpoint dir: %w", err)
	}
	lock, err := acquireQoderCheckpointLock(p)
	if err != nil {
		return fmt.Errorf("lock checkpoint: %w", err)
	}
	defer func() {
		if releaseErr := lock.release(); err == nil && releaseErr != nil {
			err = fmt.Errorf("release checkpoint lock: %w", releaseErr)
		}
	}()

	return saveQoderCheckpointLocked(stateDir, p, cp)
}

// saveQoderCheckpointLocked commits cp while the caller owns p's checkpoint
// lock. Keeping this small inner operation separate lets the lock span the
// current-cursor read as well as the atomic replacement.
func saveQoderCheckpointLocked(stateDir, p string, cp qoderCheckpoint) error {
	current := loadQoderCheckpoint(stateDir, cp.Abs)
	if current.LastLine > cp.LastLine {
		cp = current
	}
	b, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), "."+filepath.Base(p)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create checkpoint staging file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure checkpoint staging file: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync checkpoint staging file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close checkpoint staging file: %w", err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("commit checkpoint: %w", err)
	}
	return nil
}

// fileState returns the size and mtime of a transcript (for diagnostics).
func fileState(abs string) (size int64, mtime string, err error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return 0, "", err
	}
	return fi.Size(), fi.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"), nil
}
