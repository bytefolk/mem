package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
	"github.com/PeterGuy326/mem/server/internal/ingest"
	"github.com/spf13/cobra"
)

const (
	watchGiveUpCycles = 10
	watchReportCap    = 200
)

var errWatchLocked = errors.New("another watcher already holds this root")

type watchOptions struct {
	root           string
	dest           string
	interval       time.Duration
	format         string
	dryRun         bool
	limit          int
	tags           []string
	sourceMetadata *apiclient.FileSourceMetadata
	client         *httpClient
}

type fileStat struct {
	size  int64
	mtime time.Time
}

type watchItem struct {
	LocalPath    string `json:"local_path"`
	FileID       string `json:"file_id,omitempty"`
	Path         string `json:"path,omitempty"`
	SHA256Prefix string `json:"sha256_prefix,omitempty"`
	Status       string `json:"status"`
	Code         string `json:"code,omitempty"`
}

type watchReport struct {
	Scanned   int         `json:"scanned"`
	Ingested  int         `json:"ingested"`
	Deduped   int         `json:"deduped"`
	Unchanged int         `json:"unchanged"`
	Changed   int         `json:"changed"`
	LocalGone int         `json:"local_gone"`
	Failed    int         `json:"failed"`
	Items     []watchItem `json:"items"`
}

type watchSession struct {
	opts        watchOptions
	root        string
	cursorDir   string
	reportPath  string
	stdout      io.Writer
	pending     map[string]fileStat
	consecutive int
	giveUpCode  ingest.Code
	sleep       func(context.Context, time.Duration) error
}

func runPutWatch(cmd *cobra.Command, opts watchOptions) error {
	if opts.interval <= 0 {
		return errors.New("--interval must be positive")
	}
	root, err := ingest.CanonicalRoot(opts.root)
	if err != nil {
		return newCliError(1, err.Error(), "set put --watch to an accessible path")
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			cmd.SilenceUsage = true
			return newCliError(2, "watched path not found", "pass an existing directory")
		}
		return newCliError(1, err.Error(), "")
	}

	state := cliStateRoot()
	key := watchRootKey(root)
	lockPath := filepath.Join(state, "watch", "locks", key)
	lock, err := acquireWatchLock(lockPath)
	if err != nil {
		if errors.Is(err, errWatchLocked) {
			return newCliError(1, "another watcher is already running for this directory",
				"wait for the other process to exit")
		}
		return err
	}
	defer lock.release()

	sess := &watchSession{
		opts:       opts,
		root:       root,
		cursorDir:  filepath.Join(state, "watch", "cursors", key),
		reportPath: filepath.Join(state, "watch", "reports", key+".jsonl"),
		stdout:     cmd.OutOrStdout(),
		pending:    map[string]fileStat{},
		sleep: func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
				return nil
			}
		},
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return sess.loop(ctx)
}

func (s *watchSession) loop(ctx context.Context) error {
	sleep := s.sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
				return nil
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := s.cycle(context.Background()); err != nil {
			return err
		}
		if err := s.giveUpError(); err != nil {
			return err
		}
		if err := sleep(ctx, s.opts.interval); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
	}
}

func (s *watchSession) giveUpError() error {
	if s.consecutive < watchGiveUpCycles {
		return nil
	}
	switch s.giveUpCode {
	case ingest.CodeAuth:
		return newCliError(3, "watch stopped after repeated auth failures", "run `mem auth login`")
	case ingest.CodePlanQuota:
		return newCliError(4, "watch stopped after repeated plan/quota failures", "")
	case ingest.CodeProviderTimeout:
		return newCliError(5, "watch stopped after repeated provider/timeout failures", "")
	default:
		return newCliError(1, "watch stopped after repeated failures", "")
	}
}

func (s *watchSession) cycle(ctx context.Context) error {
	report := watchReport{Items: []watchItem{}}
	paths, err := ingest.Walk(s.root, nil)
	if err != nil {
		return err
	}

	live := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		live[p] = struct{}{}
	}

	cursors, err := loadWatchCursors(s.cursorDir)
	if err != nil {
		return err
	}
	for _, cp := range cursors {
		if cp.LastLine < 1 {
			continue
		}
		if _, ok := live[cp.Abs]; ok {
			continue
		}
		report.LocalGone++
		report.Items = append(report.Items, watchItem{
			LocalPath:    cp.Abs,
			FileID:       cp.FileID,
			Path:         cp.RemotePath,
			SHA256Prefix: shaPrefix(cp.SHA256),
			Status:       "local_gone",
		})
	}

	var toIngest []string
	for _, abs := range paths {
		report.Scanned++
		fi, err := os.Stat(abs)
		if err != nil {
			report.Failed++
			code := ingest.Classify(err)
			report.Items = append(report.Items, watchItem{
				LocalPath: abs,
				Status:    "failed",
				Code:      string(code),
			})
			continue
		}
		st := fileStat{size: fi.Size(), mtime: fi.ModTime()}
		cp := ingest.LoadCursor(s.cursorDir, abs)
		if cp.LastLine >= 1 {
			if sameStat(cp, st) {
				report.Unchanged++
				delete(s.pending, abs)
				continue
			}
			sum, err := hashFile(abs)
			if err != nil {
				report.Failed++
				report.Items = append(report.Items, watchItem{
					LocalPath: abs,
					Status:    "failed",
					Code:      string(ingest.Classify(err)),
				})
				continue
			}
			if cp.SHA256 != "" && sum == cp.SHA256 {
				report.Unchanged++
				_ = ingest.SaveCursor(s.cursorDir, ingest.Cursor{
					Abs:        abs,
					Size:       st.size,
					ModTime:    st.mtime.UTC().Format("2006-01-02T15:04:05Z07:00"),
					LastLine:   cp.LastLine,
					SHA256:     cp.SHA256,
					FileID:     cp.FileID,
					RemotePath: cp.RemotePath,
				})
				delete(s.pending, abs)
				continue
			}
			report.Changed++
			report.Items = append(report.Items, watchItem{
				LocalPath:    abs,
				FileID:       cp.FileID,
				Path:         cp.RemotePath,
				SHA256Prefix: shaPrefix(cp.SHA256),
				Status:       "changed",
			})
			delete(s.pending, abs)
			continue
		}

		prev, seen := s.pending[abs]
		if !seen || prev.size != st.size || !prev.mtime.Equal(st.mtime) {
			s.pending[abs] = st
			continue
		}
		toIngest = append(toIngest, abs)
	}
	for pending := range s.pending {
		if _, ok := live[pending]; !ok {
			delete(s.pending, pending)
		}
	}

	records := map[string]uploadRecord{}
	fatalAttempts := 0
	fatalCode := ingest.Code("")
	nonFatalOnly := true
	attempts := 0

	for _, abs := range toIngest {
		attempts++
		rec := uploadRecord{}
		core, err := ingest.Run(ctx, []string{abs}, ingest.Options{
			StateDir: s.cursorDir,
			DryRun:   s.opts.dryRun,
			Limit:    s.opts.limit,
		}, parseWatchFile, s.uploadWatchFile(&rec))
		if rec.local != "" {
			records[abs] = rec
		}
		if err != nil {
			code := ingest.Classify(err)
			if core.Failures != nil {
				for c := range core.Failures {
					code = c
					break
				}
			}
			report.Failed++
			report.Items = append(report.Items, watchItem{
				LocalPath: abs,
				Status:    "failed",
				Code:      string(code),
			})
			if isGiveUpCode(code) {
				fatalAttempts++
				fatalCode = code
			} else {
				nonFatalOnly = false
			}
			continue
		}
		if s.opts.dryRun {
			report.Ingested += core.Ingested
			report.Items = append(report.Items, watchItem{
				LocalPath: abs,
				Status:    "ingested",
			})
			delete(s.pending, abs)
			continue
		}
		nonFatalOnly = false
		item := watchItem{
			LocalPath:    abs,
			FileID:       rec.fileID,
			Path:         rec.remotePath,
			SHA256Prefix: shaPrefix(rec.sha256),
			Status:       "ingested",
		}
		if rec.deduped {
			report.Deduped++
			item.Status = "deduped"
		} else {
			report.Ingested++
		}
		if rec.sha256 != "" {
			size, mtime, _ := ingest.FileState(abs)
			_ = ingest.SaveCursor(s.cursorDir, ingest.Cursor{
				Abs:        abs,
				Size:       size,
				ModTime:    mtime,
				LastLine:   1,
				SHA256:     rec.sha256,
				FileID:     rec.fileID,
				RemotePath: rec.remotePath,
			})
		}
		report.Items = append(report.Items, item)
		delete(s.pending, abs)
	}

	if attempts > 0 && fatalAttempts == attempts && nonFatalOnly {
		s.consecutive++
		s.giveUpCode = fatalCode
	} else if attempts > 0 {
		s.consecutive = 0
		s.giveUpCode = ""
	}

	if err := writeWatchReport(s.stdout, s.opts.format, report); err != nil {
		return err
	}
	return appendWatchReport(s.reportPath, report)
}

type uploadRecord struct {
	local      string
	fileID     string
	remotePath string
	sha256     string
	deduped    bool
}

func parseWatchFile(abs string, skipBefore int) ([]ingest.Unit, int, error) {
	if skipBefore >= 1 {
		return nil, 0, nil
	}
	return []ingest.Unit{{Line: 1, Body: abs}}, 0, nil
}

func (s *watchSession) uploadWatchFile(rec *uploadRecord) ingest.UploadFunc {
	return func(ctx context.Context, abs string, u ingest.Unit) (ingest.Outcome, error) {
		if s.opts.client == nil {
			return ingest.Outcome{}, errors.New("watch upload client is not configured")
		}
		f, err := os.Open(abs)
		if err != nil {
			return ingest.Outcome{}, err
		}
		defer f.Close()
		sum := sha256.New()
		tee := io.TeeReader(f, sum)
		rel, err := filepath.Rel(s.root, abs)
		if err != nil {
			rel = filepath.Base(abs)
		}
		folder := s.opts.dest
		if d := filepath.Dir(rel); d != "" && d != "." {
			folder = joinFolder(s.opts.dest, filepath.ToSlash(d))
		}
		name := filepath.Base(abs)
		mimeType := mime.TypeByExtension(filepath.Ext(name))
		var resp map[string]any
		if err := s.opts.client.api.UploadMultipartWithSourceMetadata(
			ctx, name, mimeType, folder, tee, s.opts.tags, s.opts.sourceMetadata, &resp,
		); err != nil {
			return ingest.Outcome{}, err
		}
		fileObj, _ := resp["file"].(map[string]any)
		rec.local = abs
		rec.sha256 = hex.EncodeToString(sum.Sum(nil))
		if fileObj != nil {
			rec.fileID, _ = fileObj["id"].(string)
			rec.remotePath, _ = fileObj["path"].(string)
			if rec.remotePath == "" {
				if p, ok := fileObj["path"].(string); ok {
					rec.remotePath = p
				}
			}
		}
		deduped, _ := resp["deduped"].(bool)
		rec.deduped = deduped
		return ingest.Outcome{Deduplicated: deduped}, nil
	}
}

func sameStat(cp ingest.Cursor, st fileStat) bool {
	if cp.Size != st.size {
		return false
	}
	if cp.ModTime == "" {
		return false
	}
	return cp.ModTime == st.mtime.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func shaPrefix(sum string) string {
	if len(sum) < 12 {
		return sum
	}
	return sum[:12]
}

func watchRootKey(root string) string {
	sum := sha1.Sum([]byte(root))
	return hex.EncodeToString(sum[:])
}

func isGiveUpCode(code ingest.Code) bool {
	switch code {
	case ingest.CodeAuth, ingest.CodePlanQuota, ingest.CodeProviderTimeout:
		return true
	default:
		return false
	}
}

func loadWatchCursors(stateDir string) ([]ingest.Cursor, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []ingest.Cursor
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(stateDir, e.Name()))
		if err != nil {
			continue
		}
		var cp ingest.Cursor
		if json.Unmarshal(b, &cp) != nil || cp.Abs == "" {
			continue
		}
		out = append(out, cp)
	}
	return out, nil
}
