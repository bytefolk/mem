package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func uploadMockServer(t *testing.T, requests *atomic.Int32, dedup bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/files" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		status := "new"
		if dedup {
			status = "秒传"
		}
		resp := map[string]any{
			"file": map[string]any{
				"id":           fmt.Sprintf("file-%d", requests.Load()),
				"name":         "test",
				"size":         100,
				"sha256":       "abc123",
				"mime":         "text/plain",
				"path":         "/Uploaded/test",
				"index_status": status,
			},
			"deduped": dedup,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func setupWatchEnv(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_WORKSPACE", "ws-1")
	t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))
	return dir
}

func TestWatchNonExistentRoot(t *testing.T) {
	srv := uploadMockServer(t, &atomic.Int32{}, false)
	defer srv.Close()
	setupWatchEnv(t, srv)

	root := newRootCmd()
	root.SetArgs([]string{"put", "/nonexistent/path/xyz", "--watch"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for non-existent root")
	}
	var ce *cliError
	if !isCliError(err, &ce) || ce.code != 2 {
		t.Fatalf("err = %v, want exit code 2", err)
	}
}

func TestWatchUploadsNewFiles(t *testing.T) {
	var requests atomic.Int32
	srv := uploadMockServer(t, &requests, false)
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	os.WriteFile(filepath.Join(watchDir, "a.txt"), []byte("hello"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms", "--to", "/Dest"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(300 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Fatal("watch returned error:", err, "\noutput:", stdout.String())
	}
	if requests.Load() < 1 {
		t.Fatalf("expected at least 1 upload, got %d\noutput: %s", requests.Load(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "ingested") {
		t.Errorf("expected 'ingested' in output, got: %s", stdout.String())
	}
}

func TestWatchStabilityRequiresTwoScans(t *testing.T) {
	var requests atomic.Int32
	srv := uploadMockServer(t, &requests, false)
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(80 * time.Millisecond)
	os.WriteFile(filepath.Join(watchDir, "new.txt"), []byte("content"), 0o644)

	time.Sleep(250 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if requests.Load() < 1 {
		t.Fatalf("expected file to be uploaded after stability, got %d requests", requests.Load())
	}
}

func TestWatchChangedHashReportsOnly(t *testing.T) {
	var requests atomic.Int32
	srv := uploadMockServer(t, &requests, false)
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	original := filepath.Join(watchDir, "doc.txt")
	os.WriteFile(original, []byte("version1"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(300 * time.Millisecond)
	initialRequests := requests.Load()
	if initialRequests < 1 {
		cancel()
		<-errCh
		t.Fatalf("expected initial upload, got %d requests", initialRequests)
	}

	os.WriteFile(original, []byte("version2-different-content"), 0o644)
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	afterModifyRequests := requests.Load()
	if afterModifyRequests > initialRequests {
		t.Errorf("changed file triggered %d additional upload(s); expected 0 (report only)",
			afterModifyRequests-initialRequests)
	}
	if !strings.Contains(stdout.String(), "changed") {
		t.Errorf("expected 'changed' in output, got: %s", stdout.String())
	}
}

func TestWatchLocalGoneNoDelete(t *testing.T) {
	var requests atomic.Int32
	var deleteCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"file":{"id":"f-%d","name":"x","path":"/x","sha256":"a","size":1,"mime":"text/plain","index_status":"new"},"deduped":false}`, requests.Load())
	}))
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	vanishFile := filepath.Join(watchDir, "vanish.txt")
	os.WriteFile(vanishFile, []byte("gone soon"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(300 * time.Millisecond)
	os.Remove(vanishFile)
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	if deleteCalls.Load() != 0 {
		t.Errorf("watcher issued %d DELETE call(s); expected 0 (no deletion propagation)", deleteCalls.Load())
	}
	if !strings.Contains(stdout.String(), "local_gone") {
		t.Errorf("expected 'local_gone' in output, got: %s", stdout.String())
	}
}

func TestWatchDedupedCarriesServerPath(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"file":{"id":"existing-42","name":"dup.txt","path":"/OtherPlace/dup.txt","sha256":"x","size":1,"mime":"text/plain","index_status":"秒传"},"deduped":true}`))
	}))
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	os.WriteFile(filepath.Join(watchDir, "dup.txt"), []byte("dedup-content"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	if !strings.Contains(stdout.String(), "deduped") {
		t.Errorf("expected 'deduped' in output, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "existing-42") {
		t.Errorf("expected server-returned file id 'existing-42' in output, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "/OtherPlace/dup.txt") {
		t.Errorf("expected server-returned path '/OtherPlace/dup.txt' in output, got: %s", stdout.String())
	}
}

func TestWatchReportPersistence(t *testing.T) {
	var requests atomic.Int32
	srv := uploadMockServer(t, &requests, false)
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	os.WriteFile(filepath.Join(watchDir, "a.txt"), []byte("data"), 0o644)

	stateDir := filepath.Join(dir, "state")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	reportDir := filepath.Join(stateDir, "watch", "reports")
	entries, err := os.ReadDir(reportDir)
	if err != nil {
		t.Fatalf("report dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one report file")
	}
	reportFile := filepath.Join(reportDir, entries[0].Name())
	b, _ := os.ReadFile(reportFile)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 {
		t.Fatal("report file is empty")
	}
	var report cycleReport
	if err := json.Unmarshal([]byte(lines[0]), &report); err != nil {
		t.Fatalf("parse first report line: %v", err)
	}
	if report.Counts.Scanned < 1 {
		t.Errorf("expected scanned >= 1, got %d", report.Counts.Scanned)
	}
}

func TestWatchSingleInstanceLock(t *testing.T) {
	var requests atomic.Int32
	srv := uploadMockServer(t, &requests, false)
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root1 := newRootCmd()
	root1.SetContext(ctx)
	root1.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})
	var stdout1 bytes.Buffer
	root1.SetOut(&stdout1)
	root1.SetErr(&stdout1)

	errCh := make(chan error, 1)
	go func() { errCh <- root1.Execute() }()

	time.Sleep(100 * time.Millisecond)

	root2 := newRootCmd()
	root2.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms"})
	err := root2.Execute()
	if err == nil {
		cancel()
		<-errCh
		t.Fatal("expected lock error for second watcher")
	}
	var ce *cliError
	if !isCliError(err, &ce) || ce.code != 1 {
		t.Errorf("err = %v, want exit code 1", err)
	}

	cancel()
	<-errCh
}

func TestWatchFormatJSON(t *testing.T) {
	var requests atomic.Int32
	srv := uploadMockServer(t, &requests, false)
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	os.WriteFile(filepath.Join(watchDir, "a.txt"), []byte("data"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetContext(ctx)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "50ms", "--format", "json"})

	errCh := make(chan error, 1)
	go func() { errCh <- root.Execute() }()

	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	output := stdout.String()
	if !strings.Contains(output, `"counts"`) {
		t.Errorf("expected JSON output with 'counts' key, got: %s", output)
	}
	if !strings.Contains(output, `"scanned"`) {
		t.Errorf("expected JSON output with 'scanned' key, got: %s", output)
	}
}

func TestComputeSHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	os.WriteFile(p, []byte("hello world"), 0o644)

	got, err := computeSHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte("hello world"))
	want := hex.EncodeToString(h[:])
	if got != want {
		t.Errorf("computeSHA256 = %s, want %s", got, want)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cur := watchCursor{
		Abs:        "/tmp/test.txt",
		Size:       42,
		ModTime:    "2026-01-01T00:00:00Z",
		SHA256:     "abc123",
		FileID:     "f-1",
		IngestedAt: "2026-01-01T00:00:00Z",
	}
	if err := saveWatchCursor(dir, cur); err != nil {
		t.Fatal(err)
	}
	loaded, ok := loadWatchCursor(dir, cur.Abs)
	if !ok {
		t.Fatal("cursor not found after save")
	}
	if loaded.FileID != cur.FileID || loaded.SHA256 != cur.SHA256 || loaded.Size != cur.Size {
		t.Errorf("loaded = %+v, want %+v", loaded, cur)
	}
}

func TestLoadAllWatchCursors(t *testing.T) {
	dir := t.TempDir()
	c1 := watchCursor{Abs: "/a.txt", FileID: "f-1", SHA256: "h1"}
	c2 := watchCursor{Abs: "/b.txt", FileID: "f-2", SHA256: "h2"}
	_ = saveWatchCursor(dir, c1)
	_ = saveWatchCursor(dir, c2)

	all := loadAllWatchCursors(dir)
	if len(all) != 2 {
		t.Fatalf("expected 2 cursors, got %d", len(all))
	}
	if all["/a.txt"].FileID != "f-1" {
		t.Errorf("cursor /a.txt = %+v", all["/a.txt"])
	}
}

func TestClassifyUploadError(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("connection refused"), failureNetwork},
		{fmt.Errorf("dial tcp: lookup"), failureNetwork},
		{fmt.Errorf("something else"), failureUploadRejected},
	}
	for _, tt := range tests {
		got := classifyUploadError(tt.err)
		if got != tt.want {
			t.Errorf("classifyUploadError(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestUpdateFailCounter(t *testing.T) {
	allFail := cycleReport{
		Counts: cycleCounts{Scanned: 1, Failed: 1},
		Items:  []cycleItem{{Status: "failed", FailureCode: failureAuth}},
	}
	if got := updateFailCounter(0, allFail); got != 1 {
		t.Errorf("counter after all-fail = %d, want 1", got)
	}
	if got := updateFailCounter(9, allFail); got != 10 {
		t.Errorf("counter after 9+1 all-fail = %d, want 10", got)
	}

	mixed := cycleReport{
		Counts: cycleCounts{Scanned: 2, Failed: 1, Unchanged: 1},
	}
	if got := updateFailCounter(5, mixed); got != 0 {
		t.Errorf("counter resets on non-all-fail = %d, want 0", got)
	}

	retryable := cycleReport{
		Counts: cycleCounts{Scanned: 1, Failed: 1},
		Items:  []cycleItem{{Status: "failed", FailureCode: failureNetwork}},
	}
	if got := updateFailCounter(5, retryable); got != 0 {
		t.Errorf("counter resets on retryable failure = %d, want 0", got)
	}
}

func TestPersistReportCap(t *testing.T) {
	dir := t.TempDir()
	absRoot := "/test/root"
	for i := 0; i < maxReportLines+50; i++ {
		r := cycleReport{
			Timestamp: fmt.Sprintf("2026-01-01T00:%02d:00Z", i),
			Counts:    cycleCounts{Scanned: 1},
		}
		if err := persistReport(dir, absRoot, r); err != nil {
			t.Fatal(err)
		}
	}
	p := reportPath(dir, absRoot)
	b, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != maxReportLines {
		t.Errorf("report lines = %d, want %d", len(lines), maxReportLines)
	}
}

func isCliError(err error, ce **cliError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, ce)
}

func TestWatchGiveUpAfterTenFailCycles(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer srv.Close()
	dir := setupWatchEnv(t, srv)

	watchDir := filepath.Join(dir, "watch")
	os.MkdirAll(watchDir, 0o755)
	os.WriteFile(filepath.Join(watchDir, "a.txt"), []byte("data"), 0o644)

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"put", watchDir, "--watch", "--interval", "10ms"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected give-up error after 10 consecutive all-fail cycles")
	}
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != 3 {
		t.Errorf("err = %v, want exit code 3 (auth)", err)
	}
}
