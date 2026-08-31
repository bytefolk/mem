package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
	"github.com/PeterGuy326/mem/server/internal/ingest"
)

// writeTranscript writes a small valid JSONL transcript containing three
// ingestible conversation turns with realistic Qoder-style fields.
func writeTranscript(t *testing.T, dir, name string) string {
	t.Helper()
	lines := []string{
		`{"role":"system","content":"you are a scoring assistant","model":"qoder-default"}`,
		`{"role":"user","content":"Score candidate A on FY27 campus recruiting","timestamp":"2026-08-20T09:00:00Z"}`,
		`{"role":"assistant","content":"Candidate A: score 4/5, strong leadership evidence.","model":"claude-4-5","created_at":1724169600}`,
		`not-json-garbage`,
		`{"role":"user","content":"","model":"x"}`,
	}
	// line 4 (not-json-garbage) is unparseable; line 5 has empty content — both
	// must be dropped by the parser.
	body := strings.Join(lines, "\n")
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseQoderTranscript(t *testing.T) {
	dir := t.TempDir()
	abs := writeTranscript(t, dir, "campus-2027/sessions/recruit-s3e0a.jsonl")

	turns, skipped, err := parseQoderTranscript(abs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 {
		t.Fatalf("turns = %d, want 3\n%v", len(turns), turns)
	}
	// line 4 (garbage) and line 5 (empty content) are unparseable.
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
	if turns[0].Content != "you are a scoring assistant" || turns[0].Role != "system" ||
		turns[0].AgentID != "qoder-default" {
		t.Errorf("turn0 = %+v", turns[0])
	}
	if turns[1].Content != "Score candidate A on FY27 campus recruiting" || turns[1].Role != "user" {
		t.Errorf("turn1 = %+v", turns[1])
	}
	if turns[1].EventAt == nil || turns[1].EventAt.Format("2006-01-02") != "2026-08-20" {
		t.Errorf("turn1 eventAt = %v", turns[1].EventAt)
	}
	if turns[2].AgentID != "claude-4-5" {
		t.Errorf("turn2 = %+v", turns[2])
	}
	// line numbers must be 1-based and skip non-JSON / empty-content lines
	if turns[1].Line != 2 || turns[2].Line != 3 {
		t.Errorf("lines = %d,%d", turns[1].Line, turns[2].Line)
	}
}

func TestParseQoderTranscriptSkipBefore(t *testing.T) {
	dir := t.TempDir()
	abs := writeTranscript(t, dir, "p/s.jsonl")

	// Skip the first two ingestible lines (system + first user turn): lines 1-2.
	// The assistant decision line (3) must remain.
	turns, _, err := parseQoderTranscript(abs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	if !strings.Contains(turns[0].Content, "leadership evidence") {
		t.Errorf("turn = %+v", turns[0])
	}
	// epoch-second created_at on the assistant line must have parsed.
	if turns[0].EventAt == nil {
		t.Errorf("expected epoch timestamp to parse, got nil")
	}
}

func TestIngestQoderPostsAndCheckpoints(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "store")
	abs := writeTranscript(t, transcriptDir, "campus-2027/sessions/recruit-s3e0a.jsonl")

	var (
		mu       sync.Mutex
		bodies   []map[string]any
		keys     []string
		requests atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/memories" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		bodies = append(bodies, b)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":false}`))
	}))
	defer srv.Close()

	stateDir := filepath.Join(dir, "state")
	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_WORKSPACE", "ws-1")
	t.Setenv("MEM_STATE_DIR", stateDir)

	run := func() (string, error) {
		root := newRootCmd()
		var stdout bytes.Buffer
		root.SetOut(&stdout)
		root.SetErr(&stdout)
		root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
		return stdout.String(), root.Execute()
	}

	// First run: three memories must be posted.
	if out, err := run(); err != nil {
		t.Fatal(out, err)
	}
	if requests.Load() != 3 {
		t.Fatalf("first run requests = %d, want 3", requests.Load())
	}

	mu.Lock()
	if len(bodies) != 3 {
		t.Fatalf("bodies = %d", len(bodies))
	}
	// source flags + producer + path + kind
	b := bodies[1] // user turn
	src, _ := b["source"].(map[string]any)
	if src["type"] != "qoder" || src["ref"] != abs {
		t.Errorf("source = %#v", src)
	}
	if loc, _ := src["locator"].(map[string]any); loc["line"].(float64) != 2 {
		t.Errorf("locator = %#v", src["locator"])
	}
	prod, _ := b["producer"].(map[string]any)
	if prod["session_id"] != "recruit-s3e0a" {
		t.Errorf("producer = %#v", prod)
	}
	// the user turn has no model; the system turn (body[0]) must carry the model id
	prod0, _ := bodies[0]["producer"].(map[string]any)
	if prod0["agent_id"] != "qoder-default" {
		t.Errorf("producer[0] = %#v", prod0)
	}
	if b["kind"] != "observation" {
		t.Errorf("kind = %v", b["kind"])
	}
	if !strings.HasPrefix(b["path"].(string), "/AgentTranscripts/campus-2027/recruit-s3e0a") {
		t.Errorf("path = %v", b["path"])
	}
	if _, exists := b["idempotency_key"]; exists {
		t.Errorf("idempotency_key must not be in body: %#v", b)
	}
	// every write must carry a stable idempotency key
	for i, k := range keys {
		if !strings.HasPrefix(k, "qoder:") {
			t.Errorf("key[%d] = %q", i, k)
		}
	}
	mu.Unlock()

	// Second run: checkpoint advances, so nothing newer is posted again.
	requests.Store(0)
	if out, err := run(); err != nil {
		t.Fatal(out, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("second run requests = %d, want 0 (checkpoint must skip already-ingested lines)", requests.Load())
	}

	// Append a new line and confirm only it is ingested.
	f, err := os.OpenFile(abs, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"role":"assistant","content":"Decision: adopt the 4-score rubric.","model":"claude-4-5"}` + "\n")
	_ = f.Close()

	requests.Store(0)
	if out, err := run(); err != nil {
		t.Fatal(out, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("post-append requests = %d, want 1", requests.Load())
	}
	mu.Lock()
	last := bodies[len(bodies)-1]
	mu.Unlock()
	if !strings.Contains(last["content"].(string), "4-score rubric") {
		t.Errorf("appended memory content = %v", last["content"])
	}
}

func TestIngestQoderDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "store")
	writeTranscript(t, transcriptDir, "p/s.jsonl")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":false}`))
	}))
	defer srv.Close()

	stateDir := filepath.Join(dir, "state")
	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_STATE_DIR", stateDir)

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir, "--dry-run"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatalf("dry-run made %d request(s)", requests.Load())
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	// No checkpoint files must be written after a dry run.
	checkpointFiles, _ := filepath.Glob(filepath.Join(stateDir, "ingest", "qoder", "*.json"))
	if len(checkpointFiles) != 0 {
		t.Fatalf("dry-run left %d checkpoint file(s): %v", len(checkpointFiles), checkpointFiles)
	}

	// A dry run must not consume the transcript: the following real run has to
	// post every ingestible line. (Fresh root: cobra retains parsed flag values.)
	real := newRootCmd()
	real.SetOut(&stdout)
	real.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
	if err := real.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("requests after dry-run + real run = %d, want 3 (dry run advanced the cursor)", got)
	}
}

func TestIngestQoderRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "store")
	writeTranscript(t, transcriptDir, "p/s.jsonl")

	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_TOKEN", "") // not logged in
	t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))

	root := newRootCmd()
	root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected login error")
	}
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != 3 {
		t.Fatalf("err = %v", err)
	}
}

func TestIngestQoderReplayedResponseCounted(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "store")
	writeTranscript(t, transcriptDir, "p/s.jsonl")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":true}`))
	}))
	defer srv.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_WORKSPACE", "ws-1")
	t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))

	root := newRootCmd()
	root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d, want 3", requests.Load())
	}
}

func TestIngestQoder409ConflictDegradesPerFile(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "store")
	abs1 := writeTranscript(t, transcriptDir, "project-a/s1.jsonl")
	abs2 := writeTranscript(t, transcriptDir, "project-b/s2.jsonl")

	var (
		requests atomic.Int32
		first409 int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		src := b["source"].(map[string]any)
		loc := src["locator"].(map[string]any)
		line := int(loc["line"].(float64))
		if line == 2 && atomic.CompareAndSwapInt32(&first409, 0, 1) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"idempotency conflict","hint":"file rewritten"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":false}`))
	}))
	defer srv.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_WORKSPACE", "ws-1")
	t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))

	stateDir := filepath.Join(dir, "state")
	cpDir := filepath.Join(stateDir, "ingest", "qoder")
	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// project-a: line 1 (200), line 2 (409 → skip file, line 3 not attempted)
	// project-b: line 1 (200), line 2 (200), line 3 (200) — all 3
	// Total: 1 + 1(409) + 3 = 5 requests.
	if requests.Load() != 5 {
		t.Fatalf("requests = %d, want 5 (a1=200, a2=409→skip, b1-b3=200)", requests.Load())
	}
	if !strings.Contains(stdout.String(), "idempotency conflict") {
		t.Fatalf("expected conflict warning, got stdout = %q", stdout.String())
	}
	cp := ingest.LoadCursor(cpDir, abs1)
	if cp.LastLine != 1 {
		t.Fatalf("project-a checkpoint LastLine = %d, want 1 (line 2 failed)", cp.LastLine)
	}
	cp2 := ingest.LoadCursor(cpDir, abs2)
	if cp2.LastLine != 3 {
		t.Fatalf("project-b checkpoint LastLine = %d, want 3", cp2.LastLine)
	}
}

func TestIngestQoderLimitCheckpoint(t *testing.T) {
	dir := t.TempDir()
	transcriptDir := filepath.Join(dir, "store")
	abs := writeTranscript(t, transcriptDir, "p/s.jsonl")

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":false}`))
	}))
	defer srv.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_WORKSPACE", "ws-1")
	t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))

	stateDir := filepath.Join(dir, "state")
	cpDir := filepath.Join(stateDir, "ingest", "qoder")

	// First run with --limit 2: only 2 of 3 lines are ingested.
	root := newRootCmd()
	root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir, "--limit", "2"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("first run requests = %d, want 2", requests.Load())
	}
	cp := ingest.LoadCursor(cpDir, abs)
	if cp.LastLine != 2 {
		t.Fatalf("after limit checkpoint LastLine = %d, want 2", cp.LastLine)
	}

	// Second run without limit: processes the remaining line 3.
	requests.Store(0)
	root2 := newRootCmd()
	root2.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("second run requests = %d, want 1", requests.Load())
	}
	cp2 := ingest.LoadCursor(cpDir, abs)
	if cp2.LastLine != 3 {
		t.Fatalf("after second run LastLine = %d, want 3", cp2.LastLine)
	}
}

// TestIngestQoderUploadErrorsStayTyped covers the adapter's error contract: the
// core derives the report's failure code from the error it is handed, so
// translating it inside uploadMemory would report every API failure as a network
// failure.
func TestIngestQoderUploadErrorsStayTyped(t *testing.T) {
	cases := []struct {
		name   string
		status int
		// apiError expects a *apiclient.APIError with this status to survive.
		apiError    bool
		degrade     bool
		want        ingest.Code
		unreachable bool
	}{
		{name: "400", status: http.StatusBadRequest, apiError: true, want: ingest.CodeUploadRejected},
		{name: "401", status: http.StatusUnauthorized, apiError: true, want: ingest.CodeAuth},
		{name: "402", status: http.StatusPaymentRequired, apiError: true, want: ingest.CodePlanQuota},
		{name: "403", status: http.StatusForbidden, apiError: true, want: ingest.CodeAuth},
		{name: "409", status: http.StatusConflict, degrade: true, want: ingest.CodeUploadRejected},
		{name: "429", status: http.StatusTooManyRequests, apiError: true, want: ingest.CodePlanQuota},
		{name: "502", status: http.StatusBadGateway, apiError: true, want: ingest.CodeProviderTimeout},
		{name: "503", status: http.StatusServiceUnavailable, apiError: true, want: ingest.CodeProviderTimeout},
		{name: "504", status: http.StatusGatewayTimeout, apiError: true, want: ingest.CodeProviderTimeout},
		{name: "network", unreachable: true, want: ingest.CodeNetwork},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = fmt.Fprintf(w, `{"error":"%d","hint":"h"}`, tc.status)
		}))
		url := srv.URL
		if tc.unreachable {
			srv.Close()
		} else {
			defer srv.Close()
		}

		client := newHTTPClient(&cliConfig{Server: url, Token: "tok"})
		upload := (ingestOptions{}).uploadMemory(client, func(string, ...any) {})
		_, err := upload(context.Background(), "/store/p/s.jsonl", ingest.Unit{
			Line: 2, Body: map[string]any{"kind": "observation", "content": "c"},
		})
		if err == nil {
			t.Fatalf("%s: want an error", tc.name)
		}

		var ae *apiclient.APIError
		switch {
		case tc.apiError && !errors.As(err, &ae):
			t.Errorf("%s: err = %v, want the typed APIError to survive the adapter", tc.name, err)
		case tc.apiError && ae.StatusCode != tc.status:
			t.Errorf("%s: APIError.StatusCode = %d, want %d", tc.name, ae.StatusCode, tc.status)
		case tc.degrade && !errors.Is(err, ingest.ErrDegradeFile):
			t.Errorf("%s: err = %v, want per-file degradation", tc.name, err)
		case tc.unreachable && errors.As(err, &ae):
			t.Errorf("%s: err = %v, want a transport error", tc.name, err)
		}
		if got := ingest.Classify(err); got != tc.want {
			t.Errorf("%s: Classify = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestIngestQoderReadFailuresClassify covers the other side of a run: a source
// that cannot be read must reach the core as the OS error it is, so the report
// names the read state instead of claiming a transport failure.
func TestIngestQoderReadFailuresClassify(t *testing.T) {
	dir := t.TempDir()
	parse := ingestOptions{}.parseTranscript(dir)

	run := func(path string, wantCode ingest.Code) {
		t.Helper()
		report, err := ingest.Run(context.Background(), []string{path},
			ingest.Options{StateDir: filepath.Join(dir, "state")},
			parse,
			func(context.Context, string, ingest.Unit) (ingest.Outcome, error) {
				t.Error("upload must not be reached for an unreadable source")
				return ingest.Outcome{}, nil
			})
		if err == nil {
			t.Fatalf("%s: run succeeded", path)
		}
		if got := ingest.Classify(err); got != wantCode {
			t.Errorf("%s: Classify(%v) = %q, want %q", path, err, got, wantCode)
		}
		if report.Failures[wantCode] != 1 {
			t.Errorf("%s: report tally = %+v, want one %q", path, report.Failures, wantCode)
		}
	}

	run(filepath.Join(dir, "gone", "p.jsonl"), ingest.CodeRootMissing)

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("mode bits do not deny a read here: the permission case needs a POSIX non-root user")
	}
	denied := writeTranscript(t, dir, "denied/s.jsonl")
	if err := os.Chmod(denied, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(denied, 0o600) }()
	run(denied, ingest.CodeReadDenied)
}

// TestIngestQoderMapsExitCodesAtTheBoundary pins the SPEC §7.1 codes that the
// command owns, which is where the APIError is allowed to become a cliError.
func TestIngestQoderMapsExitCodesAtTheBoundary(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{http.StatusBadRequest, 1},
		{http.StatusUnauthorized, 3},
		{http.StatusForbidden, 3},
		{http.StatusPaymentRequired, 4},
		{http.StatusTooManyRequests, 4},
		{http.StatusBadGateway, 5},
		{http.StatusServiceUnavailable, 5},
		{http.StatusGatewayTimeout, 5},
		{http.StatusInternalServerError, 1},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		transcriptDir := filepath.Join(dir, "store")
		writeTranscript(t, transcriptDir, "p/s.jsonl")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = fmt.Fprintf(w, `{"error":"%d","hint":"h"}`, tc.status)
		}))

		t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
		t.Setenv("MEM_SERVER", srv.URL)
		t.Setenv("MEM_TOKEN", "tok")
		t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))

		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"ingest", "qoder", "--root", transcriptDir})
		err := root.Execute()
		srv.Close()

		var ce *cliError
		if !errors.As(err, &ce) {
			t.Fatalf("status %d: err = %v, want a cliError", tc.status, err)
		}
		if ce.code != tc.want {
			t.Errorf("status %d: exit code = %d, want %d", tc.status, ce.code, tc.want)
		}
	}
}

// TestIngestQoderRelativeRootKeepsSeparateCheckpoints is the cross-working-
// directory fixture: two directories each holding an identical store/p/s.jsonl
// and sharing one checkpoint directory must not look already-ingested to the
// second run.
func TestIngestQoderRelativeRootKeepsSeparateCheckpoints(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":false}`))
	}))
	defer srv.Close()

	base := t.TempDir()
	dirA := filepath.Join(base, "a")
	dirB := filepath.Join(base, "b")
	for _, dir := range []string{dirA, dirB} {
		writeTranscript(t, filepath.Join(dir, "store"), "p/s.jsonl")
	}

	t.Setenv("MEM_CONFIG", filepath.Join(base, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_STATE_DIR", filepath.Join(base, "state"))

	run := func() int {
		t.Helper()
		requests.Store(0)
		root := newRootCmd()
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"ingest", "qoder", "--root", "store"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return int(requests.Load())
	}

	t.Chdir(dirA)
	if got := run(); got != 3 {
		t.Fatalf("first run in directory A posted %d, want 3", got)
	}

	// Same relative spelling, different files: the cursor must not be shared.
	t.Chdir(dirB)
	if got := run(); got != 3 {
		t.Fatalf("run in directory B posted %d, want 3 (both stores collided on one checkpoint)", got)
	}

	// Directory A is still complete under its own identity.
	t.Chdir(dirA)
	if got := run(); got != 0 {
		t.Fatalf("re-run in directory A posted %d, want 0", got)
	}
	// And the transcript the server was told about is an absolute path, so
	// provenance does not depend on where the CLI happened to run.
	absB, err := ingest.CanonicalRoot(filepath.Join(dirB, "store", "p", "s.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ingest.LoadCursor(filepath.Join(base, "state", "ingest", "qoder"), absB).LastLine; got != 3 {
		t.Fatalf("directory B checkpoint LastLine = %d, want 3 keyed by %s", got, absB)
	}
}

// TestIngestQoderRelativeRootKeepsProjectSplit pins the other half of root
// canonicalization: the base used to derive a memory's project and session must
// be spelled the same way as the paths the walk returned.
func TestIngestQoderRelativeRootKeepsProjectSplit(t *testing.T) {
	dir := t.TempDir()
	abs, err := ingest.CanonicalRoot(filepath.Join(dir, "store", "campus-2027", "sessions", "recruit-s3e0a.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, filepath.Join(dir, "store"), "campus-2027/sessions/recruit-s3e0a.jsonl")

	var (
		mu     sync.Mutex
		paths  []string
		refs   []string
		keys   []string
		posted int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		src, _ := b["source"].(map[string]any)
		ref, _ := src["ref"].(string)
		mu.Lock()
		paths = append(paths, fmt.Sprint(b["path"]))
		refs = append(refs, ref)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		posted++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"memory":{"id":"m-1"},"replayed":false}`))
	}))
	defer srv.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(dir, "missing.yaml"))
	t.Setenv("MEM_SERVER", srv.URL)
	t.Setenv("MEM_TOKEN", "tok")
	t.Setenv("MEM_STATE_DIR", filepath.Join(dir, "state"))

	t.Chdir(dir)
	root := newRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"ingest", "qoder", "--root", "store"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if posted != 3 {
		t.Fatalf("posted %d memories, want 3", posted)
	}
	for i := range paths {
		if !strings.HasPrefix(paths[i], "/AgentTranscripts/campus-2027/recruit-s3e0a") {
			t.Errorf("path[%d] = %q, want the project taken from the store root", i, paths[i])
		}
		if refs[i] != abs {
			t.Errorf("source.ref[%d] = %q, want the canonical path %q", i, refs[i], abs)
		}
		if !strings.HasPrefix(keys[i], "qoder:") {
			t.Errorf("key[%d] = %q", i, keys[i])
		}
	}
	// The key is derived from the same canonical identity, so it cannot change
	// when the same store is reached from another working directory.
	wantKey := ingestIdempotencyKey(abs, 2)
	if keys[1] != wantKey {
		t.Errorf("key[1] = %q, want %q", keys[1], wantKey)
	}
}
