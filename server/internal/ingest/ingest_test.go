package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
)

func writeSource(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// unitsForLines builds a parser that yields one unit per line above the cursor.
func unitsForLines(lines int) ParseFunc {
	return func(abs string, skipBefore int) ([]Unit, int, error) {
		var units []Unit
		for line := skipBefore + 1; line <= lines; line++ {
			units = append(units, Unit{Line: line, Body: fmt.Sprintf("%s#%d", filepath.Base(abs), line)})
		}
		return units, 0, nil
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	src := t.TempDir()
	states := t.TempDir()
	abs := writeSource(t, src, "a.jsonl", "x")

	var uploads int
	report, err := Run(context.Background(), []string{abs}, Options{StateDir: states, DryRun: true},
		unitsForLines(3),
		func(context.Context, string, Unit) (Outcome, error) {
			uploads++
			return Outcome{}, errors.New("dry-run must not upload")
		})
	if err != nil {
		t.Fatal(err)
	}
	if uploads != 0 {
		t.Fatalf("uploads = %d, want 0 (dry-run performed writes)", uploads)
	}
	if report.Ingested != 3 || report.Scanned != 1 {
		t.Fatalf("report = %+v, want 3 planned units in 1 scanned file", report)
	}
	entries, err := os.ReadDir(states)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry-run left cursor state behind: %v", entries)
	}
}

func TestRunRespectsLimitAndReportsDedupSeparately(t *testing.T) {
	src := t.TempDir()
	states := t.TempDir()
	abs := writeSource(t, src, "a.jsonl", "x")

	var uploaded []int
	report, err := Run(context.Background(), []string{abs}, Options{StateDir: states, Limit: 2},
		unitsForLines(5),
		func(_ context.Context, _ string, u Unit) (Outcome, error) {
			uploaded = append(uploaded, u.Line)
			// The second unit is a server-reported replay.
			return Outcome{Deduplicated: u.Line == 2}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(uploaded) != 2 || uploaded[0] != 1 || uploaded[1] != 2 {
		t.Fatalf("uploaded lines = %v, want [1 2]", uploaded)
	}
	if report.Ingested != 1 || report.Deduped != 1 {
		t.Fatalf("report = %+v, want 1 ingested and 1 deduped", report)
	}
	cp := LoadCursor(states, abs)
	if cp.LastLine != 2 {
		t.Fatalf("cursor LastLine = %d, want 2", cp.LastLine)
	}
}

func TestRunDegradesOneFileAndContinuesOthers(t *testing.T) {
	src := t.TempDir()
	states := t.TempDir()
	a := writeSource(t, src, "a.jsonl", "x")
	b := writeSource(t, src, "b.jsonl", "x")

	// Seed a's cursor at line 1 so the conflict starts from line 2.
	if err := SaveCursor(states, Cursor{Abs: a, Size: 1, ModTime: "2026-01-01T00:00:00Z", LastLine: 1}); err != nil {
		t.Fatal(err)
	}

	var uploaded []string
	report, err := Run(context.Background(), []string{a, b}, Options{StateDir: states},
		unitsForLines(3),
		func(_ context.Context, abs string, u Unit) (Outcome, error) {
			uploaded = append(uploaded, fmt.Sprintf("%s:%d", filepath.Base(abs), u.Line))
			if abs == a {
				return Outcome{}, fmt.Errorf("%w: %s:%d", ErrDegradeFile, abs, u.Line)
			}
			return Outcome{}, nil
		})
	if err != nil {
		t.Fatalf("a degraded file must not abort the run: %v", err)
	}
	want := []string{"a.jsonl:2", "b.jsonl:1", "b.jsonl:2", "b.jsonl:3"}
	if strings.Join(uploaded, ",") != strings.Join(want, ",") {
		t.Fatalf("uploads = %v, want %v", uploaded, want)
	}
	if report.Failed != 1 || report.Failures[CodeUploadRejected] != 1 {
		t.Fatalf("report = %+v, want one upload_rejected failure", report)
	}
	if got := LoadCursor(states, a).LastLine; got != 1 {
		t.Fatalf("degraded file cursor LastLine = %d, want 1 (retry must stay meaningful)", got)
	}
	if got := LoadCursor(states, b).LastLine; got != 3 {
		t.Fatalf("healthy file cursor LastLine = %d, want 3", got)
	}
}

func TestRunAbortsOnUnclassifiedUploadError(t *testing.T) {
	src := t.TempDir()
	states := t.TempDir()
	abs := writeSource(t, src, "a.jsonl", "x")

	authErr := &apiclient.APIError{StatusCode: http.StatusUnauthorized}
	_, err := Run(context.Background(), []string{abs}, Options{StateDir: states},
		unitsForLines(3),
		func(context.Context, string, Unit) (Outcome, error) {
			return Outcome{}, authErr
		})
	if !errors.Is(err, authErr) {
		t.Fatalf("err = %v, want the caller's error returned unchanged", err)
	}
	if _, err := os.Stat(CursorPath(states, abs)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("aborted run must not advance the cursor, stat err = %v", err)
	}
}

func TestCorruptCursorDegradesWithoutBlocking(t *testing.T) {
	src := t.TempDir()
	states := t.TempDir()
	abs := writeSource(t, src, "a.jsonl", "x")

	p := CursorPath(states, abs)
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	cp := LoadCursor(states, abs)
	if !cp.Corrupt || cp.LastLine != 0 {
		t.Fatalf("cursor = %+v, want Corrupt with a reset high-water mark", cp)
	}

	report, err := Run(context.Background(), []string{abs}, Options{StateDir: states},
		unitsForLines(2),
		func(context.Context, string, Unit) (Outcome, error) { return Outcome{}, nil })
	if err != nil {
		t.Fatalf("a corrupt cursor must not block the run: %v", err)
	}
	if report.Failures[CodeStateCorrupt] != 1 {
		t.Fatalf("report = %+v, want one state_corrupt failure", report)
	}
	if report.Ingested != 2 {
		t.Fatalf("report.Ingested = %d, want 2 (re-planned from line 1)", report.Ingested)
	}
}

func TestLoadCursorResetsWhenFileShrank(t *testing.T) {
	src := t.TempDir()
	states := t.TempDir()
	abs := writeSource(t, src, "a.jsonl", strings.Repeat("x", 20))

	if err := SaveCursor(states, Cursor{Abs: abs, Size: 999, ModTime: "2026-01-01T00:00:00Z", LastLine: 7}); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(states, abs).LastLine; got != 0 {
		t.Fatalf("LastLine = %d, want 0 after the file shrank below the recorded size", got)
	}

	// Growing the file keeps the high-water mark.
	if err := SaveCursor(states, Cursor{Abs: abs, Size: 1, ModTime: "2026-01-01T00:00:00Z", LastLine: 7}); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(states, abs).LastLine; got != 7 {
		t.Fatalf("LastLine = %d, want 7 while the file only grew", got)
	}
}

// TestCursorOnDiskFormat pins the persisted layout: existing users' cursors
// must stay readable, so the key names and their order are part of the contract.
func TestCursorOnDiskFormat(t *testing.T) {
	states := t.TempDir()
	abs := filepath.Join(t.TempDir(), "a.jsonl")
	want := `{"abs":"` + abs + `","size":12,"mtime":"2026-08-30T06:14:01Z","last_line":3}`

	if err := SaveCursor(states, Cursor{Abs: abs, Size: 12, ModTime: "2026-08-30T06:14:01Z", LastLine: 3}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(CursorPath(states, abs))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("cursor bytes =\n%s\nwant\n%s", b, want)
	}
	fi, err := os.Stat(CursorPath(states, abs))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("cursor mode = %o, want 600", perm)
	}
}

// TestWalkCanonicalizesRelativeBase pins the identity rule: every path Walk
// returns is absolute and symlink-resolved, so a caller-supplied relative root
// cannot make two working directories share one cursor.
func TestWalkCanonicalizesRelativeBase(t *testing.T) {
	store := t.TempDir()
	writeSource(t, store, "sessions/a.jsonl", "x")
	absBase, err := CanonicalRoot(store)
	if err != nil {
		t.Fatal(err)
	}

	t.Chdir(store)
	got, err := Walk("sessions", HasJSONLExtension)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(absBase, "sessions", "a.jsonl")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("walk returned %v, want [%s]", got, want)
	}

	// A same-named source in another working directory is a different identity,
	// so the two never share a cursor file.
	storeB := t.TempDir()
	writeSource(t, storeB, "sessions/a.jsonl", "x")
	t.Chdir(storeB)
	second, err := Walk("sessions", HasJSONLExtension)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0] == want {
		t.Fatalf("relative bases from two directories collided on %v", second)
	}
	if CursorPath("states", want) == CursorPath("states", second[0]) {
		t.Fatalf("cursor keys collide for %s and %s", want, second[0])
	}

	// A root that does not exist yet stays absolute instead of erroring, which
	// is what the missing-root contract above needs.
	absent := filepath.Join(store, "nope", "deep")
	resolved, err := CanonicalRoot(absent)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("CanonicalRoot of an absent path = %q, want absolute", resolved)
	}
	t.Chdir(store)
	if paths, err := Walk("nope/deep", HasJSONLExtension); err != nil || len(paths) != 0 {
		t.Fatalf("missing root: paths = %v, err = %v; want none, no error", paths, err)
	}
}

func TestSaveCursorKeepsCommittedProgressAndLeavesNoTempFile(t *testing.T) {
	states := t.TempDir()
	abs := filepath.Join(t.TempDir(), "a.jsonl")

	// A run that read the file earlier must not rewind one that finished first.
	if err := SaveCursor(states, Cursor{Abs: abs, Size: 200, ModTime: "2026-08-30T06:14:01Z", LastLine: 10}); err != nil {
		t.Fatal(err)
	}
	if err := SaveCursor(states, Cursor{Abs: abs, Size: 200, ModTime: "2026-08-30T06:14:02Z", LastLine: 7}); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(states, abs); got.LastLine != 10 || got.Size != 200 {
		t.Fatalf("cursor = %+v, want the committed line 10 kept", got)
	}

	// A rewrite that shrank the file is the one case allowed to rewind.
	if err := SaveCursor(states, Cursor{Abs: abs, Size: 40, ModTime: "2026-08-30T06:14:03Z", LastLine: 2}); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(states, abs).LastLine; got != 2 {
		t.Fatalf("LastLine = %d, want 2 after the source shrank", got)
	}

	entries, err := os.ReadDir(states)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		t.Fatalf("state dir = %v, want only the cursor file", entries)
	}
}

// TestSaveCursorDoesNotStageInASharedSlot pins the temporary naming. Reusing
// <cursor>.tmp gives every process writing that cursor the same staging file,
// so one run's write can land inside another's rename.
func TestSaveCursorDoesNotStageInASharedSlot(t *testing.T) {
	states := t.TempDir()
	abs := filepath.Join(t.TempDir(), "a.jsonl")
	leftover := CursorPath(states, abs) + ".tmp"
	if err := os.WriteFile(leftover, []byte("another run's staging file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SaveCursor(states, Cursor{Abs: abs, Size: 20, ModTime: "2026-08-30T06:14:01Z", LastLine: 4}); err != nil {
		t.Fatal(err)
	}
	if got := LoadCursor(states, abs).LastLine; got != 4 {
		t.Fatalf("LastLine = %d, want 4", got)
	}
	if b, err := os.ReadFile(leftover); err != nil || string(b) != "another run's staging file" {
		t.Fatalf("save consumed the shared staging file: %q, err %v", b, err)
	}
}

func TestConcurrentSaveCursorPublishesWholeCursors(t *testing.T) {
	states := t.TempDir()
	abs := filepath.Join(t.TempDir(), "a.jsonl")

	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		go func(line int) {
			defer wg.Done()
			if err := SaveCursor(states, Cursor{Abs: abs, Size: 100, ModTime: "2026-08-30T06:14:01Z", LastLine: line}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	b, err := os.ReadFile(CursorPath(states, abs))
	if err != nil {
		t.Fatal(err)
	}
	var cp Cursor
	if err := json.Unmarshal(b, &cp); err != nil {
		t.Fatalf("cursor published half-written: %v (%s)", err, b)
	}
	if cp.LastLine < 1 || cp.LastLine > 8 {
		t.Fatalf("cursor = %+v", cp)
	}
	if entries, err := os.ReadDir(states); err != nil || len(entries) != 1 {
		t.Fatalf("state dir = %v, err = %v; want one cursor file", entries, err)
	}
}

func TestWalkIsDeterministicAndSkipsUnreadable(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"c.jsonl", "nested/b.jsonl", "nested/deep/a.jsonl", "ignore.txt"} {
		writeSource(t, root, name, "x")
	}
	got, err := Walk(root, HasJSONLExtension)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("walk returned %d paths, want 3 (*.jsonl only): %v", len(got), got)
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("walk order must be lexical for stable cursors: %v", got)
	}

	// A base naming one existing file is accepted as a one-off source.
	one := got[0]
	single, err := Walk(one, HasJSONLExtension)
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 || single[0] != one {
		t.Fatalf("single-file base returned %v, want [%s]", single, one)
	}

	// A missing root is not an error today: the connector reports "no
	// transcripts matched" instead. Pin that, because surfacing it as a
	// failure here would change `mem ingest qoder`'s observable behaviour.
	missing, err := Walk(filepath.Join(root, "nope"), HasJSONLExtension)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing root: paths = %v, err = %v; want none, no error", missing, err)
	}
}

func TestClassifyCoversSharedCodes(t *testing.T) {
	cases := []struct {
		err  error
		want Code
	}{
		{&apiclient.APIError{StatusCode: http.StatusForbidden}, CodeAuth},
		{&apiclient.APIError{StatusCode: http.StatusPaymentRequired}, CodePlanQuota},
		{&apiclient.APIError{StatusCode: http.StatusTooManyRequests}, CodePlanQuota},
		{&apiclient.APIError{StatusCode: http.StatusServiceUnavailable}, CodeProviderTimeout},
		{&apiclient.APIError{StatusCode: http.StatusGatewayTimeout}, CodeProviderTimeout},
		{&apiclient.APIError{StatusCode: http.StatusConflict}, CodeUploadRejected},
		{fmt.Errorf("wrapped: %w", ErrDegradeFile), CodeUploadRejected},
		{&os.PathError{Op: "open", Err: os.ErrPermission}, CodeReadDenied},
		{&os.PathError{Op: "stat", Err: os.ErrNotExist}, CodeRootMissing},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := Classify(tc.err); got != tc.want {
			t.Errorf("Classify(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}

	// The rows above build PathErrors around sentinel errors. A real failed open
	// carries a syscall.Errno instead, and Errno implements net.Error, so only
	// this shape reproduces a file error being reported as a transport failure.
	if _, err := os.Open(filepath.Join(t.TempDir(), "absent.jsonl")); err != nil {
		if got := Classify(fmt.Errorf("open transcript: %w", err)); got != CodeRootMissing {
			t.Errorf("Classify(real open failure) = %q, want %q", got, CodeRootMissing)
		}
	} else {
		t.Fatal("opening a file that does not exist succeeded")
	}
}

func TestReportAddAggregatesAcrossCycles(t *testing.T) {
	var total Report
	total.Add(Report{Scanned: 2, Ingested: 5, Deduped: 1, Failed: 1, Failures: map[Code]int{CodeUploadRejected: 1}})
	total.Add(Report{Scanned: 1, Ingested: 2, Unchanged: 1, Failures: map[Code]int{CodeStateCorrupt: 1}})
	if total.Scanned != 3 || total.Ingested != 7 || total.Deduped != 1 || total.Failed != 1 {
		t.Fatalf("total = %+v", total)
	}
	if total.Failures[CodeUploadRejected] != 1 || total.Failures[CodeStateCorrupt] != 1 {
		t.Fatalf("failure tally = %+v", total.Failures)
	}
}
