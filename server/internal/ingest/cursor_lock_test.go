package ingest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const cursorLockHelperEnv = "MEM_CURSOR_LOCK_HELPER"

func TestCursorLockHelperProcess(t *testing.T) {
	if os.Getenv(cursorLockHelperEnv) != "1" {
		return
	}
	path := os.Getenv("MEM_CURSOR_LOCK_PATH")
	ready := os.Getenv("MEM_CURSOR_LOCK_READY")
	release := os.Getenv("MEM_CURSOR_LOCK_RELEASE")
	lock, err := acquireCursorLock(path)
	if err != nil {
		os.Stderr.WriteString("cursor lock helper: " + err.Error() + "\n")
		os.Exit(2)
	}
	defer runtime.KeepAlive(lock)
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil {
		os.Stderr.WriteString("cursor lock helper: write ready: " + err.Error() + "\n")
		os.Exit(2)
	}
	for {
		if _, err := os.Stat(release); err == nil {
			os.Exit(0)
		} else if !os.IsNotExist(err) {
			os.Stderr.WriteString("cursor lock helper: inspect release: " + err.Error() + "\n")
			os.Exit(2)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAcquireCursorLockTimesOut(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("cursor locks are unsupported on this operating system")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cursor.json")
	ready := filepath.Join(dir, "ready")
	release := filepath.Join(dir, "release")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCursorLockHelperProcess$")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		cursorLockHelperEnv+"=1",
		"MEM_CURSOR_LOCK_PATH="+path,
		"MEM_CURSOR_LOCK_READY="+ready,
		"MEM_CURSOR_LOCK_RELEASE="+release,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(release, []byte("release\n"), 0o600)
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for helper to hold the cursor lock")
		}
		time.Sleep(5 * time.Millisecond)
	}

	started := time.Now()
	if _, err := acquireCursorLockWithTimeout(path, 25*time.Millisecond); err == nil {
		t.Fatal("second cursor lock unexpectedly acquired")
	} else if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock timeout took too long: %s", elapsed)
	}
}
