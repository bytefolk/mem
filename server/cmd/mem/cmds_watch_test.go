package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeterGuy326/mem/server/internal/ingest"
)

type watchUploadLog struct {
	mu      sync.Mutex
	methods []string
	posts   int
	deletes int
	dedupe  bool
	path    string
}

func (l *watchUploadLog) record(method, path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.methods = append(l.methods, method+" "+path)
	if method == http.MethodPost {
		l.posts++
	}
	if method == http.MethodDelete {
		l.deletes++
	}
}

func newWatchFileServer(t *testing.T, log *watchUploadLog) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r.Method, r.URL.Path)
		if r.Method == http.MethodDelete {
			t.Errorf("watch must not issue DELETE %s", r.URL.Path)
			http.Error(w, "delete forbidden", http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/files" {
			http.NotFound(w, r)
			return
		}
		name := "file.bin"
		if _, hdr, err := r.FormFile("file"); err == nil {
			name = hdr.Filename
		}
		remote := log.path
		if remote == "" {
			remote = "/vault/" + name
		}
		status := http.StatusCreated
		if log.dedupe {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"file": map[string]any{
				"id":     "11111111-1111-1111-1111-111111111111",
				"name":   name,
				"path":   remote,
				"sha256": "abc",
			},
			"deduped": log.dedupe,
		})
	}))
}

func newTestWatch(t *testing.T, root string, client *httpClient) *watchSession {
	t.Helper()
	canonical, err := ingest.CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("MEM_STATE_DIR", state)
	return &watchSession{
		opts: watchOptions{
			root:     canonical,
			dest:     "/Inbox",
			interval: time.Second,
			format:   "json",
			client:   client,
		},
		root:       canonical,
		cursorDir:  filepath.Join(state, "watch", "cursors", "test"),
		reportPath: filepath.Join(state, "watch", "reports", "test.jsonl"),
		stdout:     io.Discard,
		pending:    map[string]fileStat{},
	}
}

func decodeWatchJSON(t *testing.T, raw string) watchReport {
	t.Helper()
	var report watchReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &report); err != nil {
		t.Fatalf("report json: %v (%s)", err, raw)
	}
	return report
}

func TestWatchIngestsNewFileAfterQuietInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &watchUploadLog{}
	srv := newWatchFileServer(t, log)
	defer srv.Close()
	client := newHTTPClient(&cliConfig{Server: srv.URL, Token: "tok"})
	sess := newTestWatch(t, dir, client)
	var buf bytes.Buffer
	sess.stdout = &buf

	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := decodeWatchJSON(t, buf.String())
	if first.Scanned != 1 || first.Ingested != 0 || log.posts != 0 {
		t.Fatalf("first cycle = %+v posts=%d, want pending", first, log.posts)
	}

	buf.Reset()
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := decodeWatchJSON(t, buf.String())
	if second.Ingested != 1 || log.posts != 1 {
		t.Fatalf("second cycle = %+v posts=%d, want ingest", second, log.posts)
	}
	if len(second.Items) != 1 || second.Items[0].Status != "ingested" {
		t.Fatalf("items = %+v", second.Items)
	}

	buf.Reset()
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	third := decodeWatchJSON(t, buf.String())
	if third.Unchanged != 1 || third.Ingested != 0 || log.posts != 1 {
		t.Fatalf("third cycle = %+v posts=%d, want unchanged", third, log.posts)
	}
}

func TestWatchFailureContinuesWithStableCode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"file":    map[string]any{"id": "id-ok", "path": "/Inbox/b.txt", "name": "b.txt"},
			"deduped": false,
		})
	}))
	defer srv.Close()
	sess := newTestWatch(t, dir, newHTTPClient(&cliConfig{Server: srv.URL, Token: "tok"}))
	var buf bytes.Buffer
	sess.stdout = &buf
	_ = sess.cycle(context.Background())
	buf.Reset()
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := decodeWatchJSON(t, buf.String())
	if report.Failed != 1 || report.Ingested != 1 {
		t.Fatalf("report = %+v, want 1 failed and 1 ingested", report)
	}
	var sawAuth bool
	for _, item := range report.Items {
		if item.Status == "failed" && item.Code == string(ingest.CodeAuth) {
			sawAuth = true
		}
	}
	if !sawAuth {
		t.Fatalf("items = %+v, want auth failure code", report.Items)
	}
}

func TestWatchLocalGoneDoesNotDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &watchUploadLog{}
	srv := newWatchFileServer(t, log)
	defer srv.Close()
	sess := newTestWatch(t, dir, newHTTPClient(&cliConfig{Server: srv.URL, Token: "tok"}))
	_ = sess.cycle(context.Background())
	_ = sess.cycle(context.Background())
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sess.stdout = &buf
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := decodeWatchJSON(t, buf.String())
	if report.LocalGone != 1 {
		t.Fatalf("report = %+v, want local_gone", report)
	}
	if log.deletes != 0 {
		t.Fatalf("deletes = %d", log.deletes)
	}
	for _, m := range log.methods {
		if strings.HasPrefix(m, http.MethodDelete) {
			t.Fatalf("unexpected method %s", m)
		}
	}
}

func TestWatchChangedFileIsReportedNotReingested(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &watchUploadLog{}
	srv := newWatchFileServer(t, log)
	defer srv.Close()
	sess := newTestWatch(t, dir, newHTTPClient(&cliConfig{Server: srv.URL, Token: "tok"}))
	_ = sess.cycle(context.Background())
	_ = sess.cycle(context.Background())
	posts := log.posts
	if err := os.WriteFile(path, []byte("two-different"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sess.stdout = &buf
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := decodeWatchJSON(t, buf.String())
	if report.Changed != 1 {
		t.Fatalf("report = %+v, want changed", report)
	}
	if log.posts != posts {
		t.Fatalf("posts = %d after change, want %d", log.posts, posts)
	}
	buf.Reset()
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := decodeWatchJSON(t, buf.String())
	if again.Changed != 1 || log.posts != posts {
		t.Fatalf("second changed cycle = %+v posts=%d", again, log.posts)
	}
}

func TestWatchDedupedItemUsesServerPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dup.txt"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &watchUploadLog{dedupe: true, path: "/already/elsewhere/dup.txt"}
	srv := newWatchFileServer(t, log)
	defer srv.Close()
	sess := newTestWatch(t, dir, newHTTPClient(&cliConfig{Server: srv.URL, Token: "tok"}))
	var buf bytes.Buffer
	sess.stdout = &buf
	_ = sess.cycle(context.Background())
	buf.Reset()
	if err := sess.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := decodeWatchJSON(t, buf.String())
	if report.Deduped != 1 || report.Ingested != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Items) != 1 || report.Items[0].Path != "/already/elsewhere/dup.txt" {
		t.Fatalf("item = %+v", report.Items)
	}
}

func TestWatchMissingRootExitsNotFound(t *testing.T) {
	setWorkspaceTestConfig(t, "http://127.0.0.1:1", "tok", "ws")
	root := newRootCmd()
	root.SetArgs([]string{"put", filepath.Join(t.TempDir(), "missing"), "--watch"})
	err := root.Execute()
	var ce *cliError
	if err == nil || !errors.As(err, &ce) || ce.code != 2 {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchLockFailsFast(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "lock")
	first, err := acquireWatchLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	_, err = acquireWatchLock(lockPath)
	if !errors.Is(err, errWatchLocked) {
		t.Fatalf("err = %v, want errWatchLocked", err)
	}
}

func TestWatchLoopExitsZeroOnCancel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := &watchUploadLog{}
	srv := newWatchFileServer(t, log)
	defer srv.Close()
	sess := newTestWatch(t, dir, newHTTPClient(&cliConfig{Server: srv.URL, Token: "tok"}))
	sess.opts.interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sess.loop(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop err = %v, want nil (exit 0)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch loop did not exit")
	}
}

func TestWatchReportJSONLIsCapped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.jsonl")
	for i := 0; i < 205; i++ {
		if err := appendWatchReport(path, watchReport{Scanned: i, Items: []watchItem{}}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != watchReportCap {
		t.Fatalf("lines = %d, want %d", len(lines), watchReportCap)
	}
	var last watchReport
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Scanned != 204 {
		t.Fatalf("last scanned = %d, want 204", last.Scanned)
	}
}

func TestWatchJSONHasClosedVocabulary(t *testing.T) {
	report := watchReport{
		Scanned: 1, Ingested: 1, Items: []watchItem{{
			LocalPath: "/tmp/a", FileID: "id", Path: "/Inbox/a", Status: "ingested",
		}},
	}
	var buf bytes.Buffer
	if err := writeWatchReport(&buf, "json", report); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"scanned", "ingested", "deduped", "unchanged", "changed", "local_gone", "failed", "items"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing %s", key)
		}
	}
}

func TestWatchFlagIsRegistered(t *testing.T) {
	cmd, _, err := newRootCmd().Find([]string{"put"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Flags().Lookup("watch") == nil || cmd.Flags().Lookup("interval") == nil {
		t.Fatal("put --watch/--interval flags missing")
	}
}
