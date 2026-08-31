// Package ingest owns the mechanics that every local→mem ingestion connector
// would otherwise re-implement: a deterministic recursive walk, a per-file
// incremental cursor store, the change decision, a closed failure-code
// vocabulary, and cycle report aggregation.
//
// The package is deliberately unaware of any particular input format, of HTTP,
// and of the command surface. A connector supplies two functions:
//
//	Parse  turns one local file into ordered Units, given the leading line
//	       count already ingested, and reports how many lines it could not use.
//	Upload persists one Unit and reports whether the server replayed it. Its
//	       error is one that Classify understands.
//
// Call sites stay thin: `mem ingest qoder` today wires a transcript parser and
// a /v1/memories POST to Run, and a future `mem put --watch` wires a different
// source and sink to the same Run, so cursor layout, change detection and the
// report vocabulary are written once.
//
// Run returns a Report; printing it is the caller's job, which is why nothing
// here touches cobra or an io.Writer directly. Diagnostics go through
// Options.Log when the caller wants them.
//
// Contract notes that matter for new call sites:
//
//   - The change decision is size-based, not content-hashed. A cursor records
//     the file size at write time, and a file that has since become smaller is
//     treated as rewritten so its cursor resets. Nothing compares content, so a
//     same-size in-place edit is not detected here; adding a content gate is a
//     decision to make, not an implementation detail of a call site.
//   - Cursors are keyed by the absolute path (see CursorPath). Keying on
//     path-plus-device identity would invalidate existing on-disk cursors.
//   - --dry-run neither writes a request nor advances a cursor. Callers must
//     not "optimize" by saving a cursor after a dry run.
package ingest

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
)

// Code is the closed set of failure classifications a run may report. Names are
// shared vocabulary: report consumers (CLI text, watch daemon, JSON output)
// must not invent per-call-site aliases for the same condition.
type Code string

const (
	// CodeAuth: the server rejected our credentials (401/403).
	CodeAuth Code = "auth"
	// CodePlanQuota: plan or quota blocked the write (402/429).
	CodePlanQuota Code = "plan_quota"
	// CodeProviderTimeout: an upstream provider stage failed or timed out
	// (502/503/504).
	CodeProviderTimeout Code = "provider_timeout"
	// CodeNetwork: the request never reached the server.
	CodeNetwork Code = "network"
	// CodeReadDenied: a local path could not be read.
	CodeReadDenied Code = "read_denied"
	// CodeUploadRejected: the server refused this specific unit (409 on a
	// stable idempotency key, or a rejected payload).
	CodeUploadRejected Code = "upload_rejected"
	// CodeRootMissing: the configured source root does not exist.
	CodeRootMissing Code = "root_missing"
	// CodeStateCorrupt: a cursor could not be decoded, so it was treated as
	// "nothing ingested yet" rather than blocking the run.
	CodeStateCorrupt Code = "state_corrupt"
)

// ErrDegradeFile lets an UploadFunc say "stop this file, keep the run going".
// A rewritten file can conflict with its own stable per-line keys forever, so
// aborting the whole cycle would let one bad file block every other source.
// The cursor for that file is not advanced, which keeps a retry meaningful.
var ErrDegradeFile = errors.New("ingest: degrade file")

// Unit is one ingestible item produced by a connector's Parse function.
type Unit struct {
	// Line is the 1-based position in the source file that this unit came
	// from. Run records it in the cursor as the high-water mark.
	Line int
	// Body is the request payload, opaque to this package.
	Body any
	// IdempotencyKey is the connector's stable retry key for this unit.
	IdempotencyKey string
}

// ParseFunc converts one local file into the units that have not been ingested
// yet. skipBefore is the cursor's high-water mark: units at or below it must
// not be returned. The second result counts lines that were readable but
// produced no unit (malformed, empty, or out of scope for the format).
type ParseFunc func(abs string, skipBefore int) ([]Unit, int, error)

// Outcome is what an Upload call reports back about one unit.
type Outcome struct {
	// Deduplicated marks a server-reported idempotent replay: the memory
	// already existed for this key, so nothing new was written. Counting
	// replays as ingested would overstate a re-run.
	Deduplicated bool
}

// UploadFunc persists one unit and reports whether the server treated it as a
// replay. Return an error wrapping ErrDegradeFile to skip the rest of the
// current file, or any other error to end the run.
type UploadFunc func(ctx context.Context, abs string, u Unit) (Outcome, error)

// Cursor is the persisted per-file checkpoint. The field order and JSON names
// are the on-disk format: changing either would strand cursors that existing
// users already have.
type Cursor struct {
	Abs      string `json:"abs"`
	Size     int64  `json:"size"`
	ModTime  string `json:"mtime"`
	LastLine int    `json:"last_line"`

	// Corrupt is set in memory when a stored cursor failed to decode. It is
	// never persisted.
	Corrupt bool `json:"-"`
}

// Options configures one run.
type Options struct {
	// StateDir holds the cursors. Required.
	StateDir string
	// DryRun plans only: no Upload call, no cursor write.
	DryRun bool
	// Limit stops ingesting units after this many (0 = no limit).
	Limit int
	// Log receives diagnostics. Nil discards them.
	Log func(format string, args ...any)
}

// Report aggregates one cycle. Unchanged, Changed and LocalGone stay zero for
// sources that do not observe those states; they exist so a watcher and a
// one-shot importer report the same names.
type Report struct {
	Scanned     int          // files walked and offered to Parse
	Ingested    int          // units persisted by this run (or planned, in dry-run)
	Deduped     int          // units the server reported as replays
	Unchanged   int          // files whose content was already ingested
	Changed     int          // files accepted because they moved forward
	LocalGone   int          // cursor records whose file disappeared
	Failed      int          // files degraded rather than aborted
	Unparseable int          // readable lines that yielded no unit
	Failures    map[Code]int // per-code tally, including cursor degradation
}

// Add folds another report into this one, for callers that run several batches
// and report once.
func (r *Report) Add(other Report) {
	r.Scanned += other.Scanned
	r.Ingested += other.Ingested
	r.Deduped += other.Deduped
	r.Unchanged += other.Unchanged
	r.Changed += other.Changed
	r.LocalGone += other.LocalGone
	r.Failed += other.Failed
	r.Unparseable += other.Unparseable
	for code, n := range other.Failures {
		if r.Failures == nil {
			r.Failures = map[Code]int{}
		}
		r.Failures[code] += n
	}
}

func (r *Report) fail(code Code) {
	if r.Failures == nil {
		r.Failures = map[Code]int{}
	}
	r.Failures[code]++
}

// Walk collects candidate files under base in lexical order, so cursor
// high-water marks mean the same thing from one run to the next. Go's
// filepath.Glob does not treat ** as recursive, hence the explicit walk.
// Unreadable entries are skipped rather than failing the walk: a session store
// routinely contains directories the caller cannot enter.
//
// A base that does not exist yields no paths and no error, which is what the
// existing connector does with it (it reports "no transcripts matched").
// Whether a missing root is worth reporting is therefore the call site's
// decision; CodeRootMissing exists for the sites that report it, such as a
// watch daemon that must not sit idle on a typo'd path.
//
// accept is called with each candidate path; a nil accept takes every file.
func Walk(base string, accept func(abs string) bool) ([]string, error) {
	if fi, err := os.Stat(base); err == nil && !fi.IsDir() {
		return []string{base}, nil
	}
	var paths []string
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if accept == nil || accept(p) {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// CursorPath returns the cursor file for one source path, keyed by a hash of
// its absolute path so any source layout is safe to store in one directory.
func CursorPath(stateDir, abs string) string {
	sum := sha1.Sum([]byte(abs))
	return filepath.Join(stateDir, hex.EncodeToString(sum[:])+".json")
}

// LoadCursor reads a cursor. A missing or undecodable cursor yields LastLine 0,
// meaning "nothing ingested yet", and is never a hard error: broken state must
// not block ingestion. Load reports that case through Cursor.Corrupt instead.
//
// A file that is now smaller than when its cursor was written was truncated
// and rewritten, so LastLine resets; otherwise new content at already-ingested
// line numbers would be skipped forever.
func LoadCursor(stateDir, abs string) Cursor {
	var cp Cursor
	b, err := os.ReadFile(CursorPath(stateDir, abs))
	if err != nil {
		return cp
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		return Cursor{Abs: abs, Corrupt: true}
	}
	if cp.Abs == "" {
		cp.Abs = abs
	}
	if cp.Size > 0 {
		if fi, err := os.Stat(abs); err == nil && fi.Size() < cp.Size {
			cp.LastLine = 0
		}
	}
	return cp
}

// SaveCursor writes a cursor through a temporary file and an atomic rename, so
// an interrupted run cannot leave a half-written checkpoint behind.
func SaveCursor(stateDir string, cp Cursor) error {
	p := CursorPath(stateDir, cp.Abs)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create checkpoint dir: %w", err)
	}
	b, err := json.Marshal(cp)
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("commit checkpoint: %w", err)
	}
	return nil
}

// FileState returns the size and UTC mtime recorded alongside a cursor. Both
// are diagnostics: only size participates in the change decision.
func FileState(abs string) (size int64, mtime string, err error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return 0, "", err
	}
	return fi.Size(), fi.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"), nil
}

// Classify maps an Upload or Parse failure onto the shared vocabulary. Callers
// use it to report a code without re-deriving it from statuses, and exit-code
// mapping stays in the command surface that owns it.
func Classify(err error) Code {
	if err == nil {
		return ""
	}
	var ae *apiclient.APIError
	if errors.As(err, &ae) {
		switch ae.Kind() {
		case apiclient.KindAuth:
			return CodeAuth
		case apiclient.KindPlan, apiclient.KindQuota:
			return CodePlanQuota
		case apiclient.KindProvider, apiclient.KindTimeout:
			return CodeProviderTimeout
		case apiclient.KindConflict, apiclient.KindBadInput:
			return CodeUploadRejected
		}
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, os.ErrDeadlineExceeded) {
		return CodeNetwork
	}
	switch {
	case errors.Is(err, ErrDegradeFile):
		return CodeUploadRejected
	case errors.Is(err, os.ErrPermission):
		return CodeReadDenied
	case errors.Is(err, os.ErrNotExist):
		return CodeRootMissing
	}
	return CodeNetwork
}

// Run walks the given paths, parses each against its cursor, uploads units
// through upload, and persists cursors for the files it actually wrote.
//
// A unit error other than ErrDegradeFile ends the run and is returned as-is, so
// the caller keeps its own error surface. An ErrDegradeFile error ends the
// current file only; its cursor stays put and the report records the failure.
func Run(ctx context.Context, paths []string, opts Options, parse ParseFunc, upload UploadFunc) (Report, error) {
	var report Report
	remaining := opts.Limit

	for _, abs := range paths {
		report.Scanned++

		cp := LoadCursor(opts.StateDir, abs)
		if cp.Corrupt {
			report.fail(CodeStateCorrupt)
		}
		units, unparseable, err := parse(abs, cp.LastLine)
		report.Unparseable += unparseable
		if err != nil {
			return report, err
		}

		newLast := cp.LastLine
		moved := false
		for _, u := range units {
			if opts.Limit > 0 && remaining <= 0 {
				break
			}
			// Parse already skips at or below the cursor; this guard keeps an
			// inconsistent parser from rewinding a cursor.
			if u.Line <= newLast {
				continue
			}

			if opts.DryRun {
				report.Ingested++
				if remaining > 0 {
					remaining--
				}
				continue
			}

			outcome, err := upload(ctx, abs, u)
			if err != nil {
				if errors.Is(err, ErrDegradeFile) {
					report.Failed++
					report.fail(CodeUploadRejected)
					break
				}
				report.fail(Classify(err))
				return report, err
			}
			if outcome.Deduplicated {
				report.Deduped++
			} else {
				report.Ingested++
			}
			if remaining > 0 {
				remaining--
			}
			newLast = u.Line
			moved = true
		}
		if moved {
			report.Changed++
		}

		if opts.DryRun {
			continue
		}
		if size, mtime, err := FileState(abs); err == nil {
			if err := SaveCursor(opts.StateDir, Cursor{
				Abs:      abs,
				Size:     size,
				ModTime:  mtime,
				LastLine: newLast,
			}); err != nil {
				if opts.Log != nil {
					opts.Log("warn: save checkpoint for %s: %v\n", abs, err)
				}
				report.fail(CodeStateCorrupt)
			}
		}
	}
	return report, nil
}

// HasJSONLExtension reports whether a path looks like a JSON-lines file,
// ignoring case. It is the accept predicate the transcript connectors use.
func HasJSONLExtension(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".jsonl")
}
