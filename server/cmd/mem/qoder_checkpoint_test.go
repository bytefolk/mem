package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const qoderCheckpointHelperEnv = "MEM_QODER_CHECKPOINT_HELPER"

// TestQoderCheckpointHelperProcess is re-executed as an independent process by
// the tests below. The hold-lock action exits through os.Exit rather than
// release(), deliberately modelling a process that dies while it owns the
// advisory lock.
func TestQoderCheckpointHelperProcess(t *testing.T) {
	if os.Getenv(qoderCheckpointHelperEnv) != "1" {
		return
	}

	action := os.Getenv("MEM_QODER_CHECKPOINT_ACTION")
	stateDir := os.Getenv("MEM_QODER_CHECKPOINT_STATE_DIR")
	abs := os.Getenv("MEM_QODER_CHECKPOINT_ABS")
	lastLine, err := strconv.Atoi(os.Getenv("MEM_QODER_CHECKPOINT_LAST_LINE"))
	if err != nil {
		checkpointHelperExit("parse last line: %v", err)
	}
	size, err := strconv.ParseInt(os.Getenv("MEM_QODER_CHECKPOINT_SIZE"), 10, 64)
	if err != nil {
		checkpointHelperExit("parse checkpoint size: %v", err)
	}
	started := os.Getenv("MEM_QODER_CHECKPOINT_STARTED")
	ready := os.Getenv("MEM_QODER_CHECKPOINT_READY")
	release := os.Getenv("MEM_QODER_CHECKPOINT_RELEASE")

	if started != "" {
		checkpointHelperWriteSignal(started)
	}
	cp := qoderCheckpoint{
		Abs:      abs,
		Size:     size,
		ModTime:  os.Getenv("MEM_QODER_CHECKPOINT_MOD_TIME"),
		LastLine: lastLine,
	}
	var heldLock *qoderCheckpointLock
	switch action {
	case "save":
		if err := saveQoderCheckpoint(stateDir, cp); err != nil {
			checkpointHelperExit("save checkpoint: %v", err)
		}
	case "save-hold-lock":
		path := qoderCheckpointPath(stateDir, abs)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			checkpointHelperExit("create checkpoint dir: %v", err)
		}
		lock, err := acquireQoderCheckpointLock(path)
		if err != nil {
			checkpointHelperExit("acquire checkpoint lock: %v", err)
		}
		heldLock = lock
		if err := saveQoderCheckpointLocked(stateDir, path, cp); err != nil {
			checkpointHelperExit("save locked checkpoint: %v", err)
		}
	default:
		checkpointHelperExit("unknown action %q", action)
	}

	checkpointHelperWriteSignal(ready)
	if release != "" {
		for {
			if _, err := os.Stat(release); err == nil {
				os.Exit(0)
			} else if !os.IsNotExist(err) {
				checkpointHelperExit("inspect release signal: %v", err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	runtime.KeepAlive(heldLock)
	os.Exit(0)
}

func checkpointHelperWriteSignal(path string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		checkpointHelperExit("create signal directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("ready\n"), 0o600); err != nil {
		checkpointHelperExit("write signal: %v", err)
	}
}

func checkpointHelperExit(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "qoder checkpoint helper: "+format+"\n", args...)
	os.Exit(2)
}

type qoderCheckpointHelper struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

func startQoderCheckpointHelper(t *testing.T, values map[string]string) *qoderCheckpointHelper {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQoderCheckpointHelperProcess$")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), qoderCheckpointHelperEnv+"=1")
	for key, value := range values {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start qoder checkpoint helper: %v", err)
	}
	helper := &qoderCheckpointHelper{cmd: cmd, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return helper
}

func (h *qoderCheckpointHelper) wait(t *testing.T) {
	t.Helper()
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("qoder checkpoint helper failed: %v", err)
	}
}

func waitForQoderCheckpointSignal(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect checkpoint signal %s: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for checkpoint signal %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func requireNoQoderCheckpointSignal(t *testing.T, path string, duration time.Duration) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("checkpoint writer escaped its live process lock before release: %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect checkpoint signal %s: %v", path, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writeQoderCheckpointRelease(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("release\n"), 0o600); err != nil {
		t.Fatalf("release checkpoint helper: %v", err)
	}
}

func TestSaveQoderCheckpointKeepsHighestLastLineAcrossIndependentProcesses(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	abs := filepath.Join(dir, "sessions", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	highReady := filepath.Join(dir, "high-ready")
	highRelease := filepath.Join(dir, "high-release")
	high := startQoderCheckpointHelper(t, map[string]string{
		"MEM_QODER_CHECKPOINT_ACTION":    "save",
		"MEM_QODER_CHECKPOINT_STATE_DIR": stateDir,
		"MEM_QODER_CHECKPOINT_ABS":       abs,
		"MEM_QODER_CHECKPOINT_LAST_LINE": "12",
		"MEM_QODER_CHECKPOINT_SIZE":      "33",
		"MEM_QODER_CHECKPOINT_MOD_TIME":  "2026-09-01T00:00:12Z",
		"MEM_QODER_CHECKPOINT_READY":     highReady,
		"MEM_QODER_CHECKPOINT_RELEASE":   highRelease,
	})
	waitForQoderCheckpointSignal(t, highReady)

	lowReady := filepath.Join(dir, "low-ready")
	low := startQoderCheckpointHelper(t, map[string]string{
		"MEM_QODER_CHECKPOINT_ACTION":    "save",
		"MEM_QODER_CHECKPOINT_STATE_DIR": stateDir,
		"MEM_QODER_CHECKPOINT_ABS":       abs,
		"MEM_QODER_CHECKPOINT_LAST_LINE": "4",
		"MEM_QODER_CHECKPOINT_SIZE":      "4",
		"MEM_QODER_CHECKPOINT_MOD_TIME":  "2026-09-01T00:00:04Z",
		"MEM_QODER_CHECKPOINT_READY":     lowReady,
	})
	waitForQoderCheckpointSignal(t, lowReady)
	low.wait(t)

	got := loadQoderCheckpoint(stateDir, abs)
	if got.LastLine != 12 {
		t.Fatalf("LastLine after high then stale low process = %d, want 12", got.LastLine)
	}
	if got.Size != 33 || got.ModTime != "2026-09-01T00:00:12Z" || got.Abs != abs {
		t.Fatalf("stale process regressed checkpoint diagnostics: %+v", got)
	}

	writeQoderCheckpointRelease(t, highRelease)
	high.wait(t)
}

func TestSaveQoderCheckpointSerializesAndRecoversAfterOwnerExit(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	abs := filepath.Join(dir, "sessions", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	highReady := filepath.Join(dir, "high-ready")
	highRelease := filepath.Join(dir, "high-release")
	high := startQoderCheckpointHelper(t, map[string]string{
		"MEM_QODER_CHECKPOINT_ACTION":    "save-hold-lock",
		"MEM_QODER_CHECKPOINT_STATE_DIR": stateDir,
		"MEM_QODER_CHECKPOINT_ABS":       abs,
		"MEM_QODER_CHECKPOINT_LAST_LINE": "12",
		"MEM_QODER_CHECKPOINT_SIZE":      "33",
		"MEM_QODER_CHECKPOINT_MOD_TIME":  "2026-09-01T00:00:12Z",
		"MEM_QODER_CHECKPOINT_READY":     highReady,
		"MEM_QODER_CHECKPOINT_RELEASE":   highRelease,
	})
	waitForQoderCheckpointSignal(t, highReady)

	lowStarted := filepath.Join(dir, "low-started")
	lowReady := filepath.Join(dir, "low-ready")
	low := startQoderCheckpointHelper(t, map[string]string{
		"MEM_QODER_CHECKPOINT_ACTION":    "save",
		"MEM_QODER_CHECKPOINT_STATE_DIR": stateDir,
		"MEM_QODER_CHECKPOINT_ABS":       abs,
		"MEM_QODER_CHECKPOINT_LAST_LINE": "4",
		"MEM_QODER_CHECKPOINT_SIZE":      "4",
		"MEM_QODER_CHECKPOINT_MOD_TIME":  "2026-09-01T00:00:04Z",
		"MEM_QODER_CHECKPOINT_STARTED":   lowStarted,
		"MEM_QODER_CHECKPOINT_READY":     lowReady,
	})
	waitForQoderCheckpointSignal(t, lowStarted)
	requireNoQoderCheckpointSignal(t, lowReady, 100*time.Millisecond)

	writeQoderCheckpointRelease(t, highRelease)
	high.wait(t)
	waitForQoderCheckpointSignal(t, lowReady)
	low.wait(t)

	if got := loadQoderCheckpoint(stateDir, abs).LastLine; got != 12 {
		t.Fatalf("LastLine after abandoned lock and lower writer = %d, want 12", got)
	}
	if err := saveQoderCheckpoint(stateDir, qoderCheckpoint{Abs: abs, LastLine: 13}); err != nil {
		t.Fatalf("checkpoint remained locked after owner exit: %v", err)
	}
	if got := loadQoderCheckpoint(stateDir, abs).LastLine; got != 13 {
		t.Fatalf("LastLine after recovery writer = %d, want 13", got)
	}
}
