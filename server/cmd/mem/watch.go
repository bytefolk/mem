package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
	"github.com/spf13/cobra"
)

type watchCursor struct {
	Abs        string `json:"abs"`
	Size       int64  `json:"size"`
	ModTime    string `json:"mtime"`
	SHA256     string `json:"sha256"`
	FileID     string `json:"file_id"`
	IngestedAt string `json:"ingested_at"`
}

type pendingFile struct {
	Abs     string
	Size    int64
	ModTime string
}

type watchOptions struct {
	root     string
	interval time.Duration
	toFolder string
	tags     []string
	format   string
}

func (o watchOptions) stateDir() string {
	return filepath.Join(cliStateRoot(), "watch")
}

func (o watchOptions) cursorDir() string {
	return filepath.Join(o.stateDir(), "cursors")
}

func (o watchOptions) lockPath() string {
	return filepath.Join(o.stateDir(), sha1Hex(o.root)+".lock")
}

func runWatchDaemon(cmd *cobra.Command, c *httpClient, opts watchOptions, sourceMetadata *apiclient.FileSourceMetadata) error {
	absRoot, err := filepath.Abs(opts.root)
	if err != nil {
		return err
	}
	fi, err := os.Stat(absRoot)
	if err != nil {
		return newCliError(2, fmt.Sprintf("watch root: %v", err), "")
	}
	if !fi.IsDir() {
		return newCliError(2, "watch root is not a directory", "pass a directory path to --watch")
	}

	lock, err := acquireWatchLock(opts.lockPath())
	if err != nil {
		return newCliError(1, "another watcher is already running for this root", "")
	}
	defer lock.release()

	cursors := loadAllWatchCursors(opts.cursorDir())
	pending := make(map[string]*pendingFile)
	consecutiveFails := 0

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCycle := func() (bool, error) {
		report := scanAndUpload(ctx, cmd, c, opts, absRoot, cursors, pending, sourceMetadata)
		printReport(cmd, report, opts.format)
		if perr := persistReport(opts.stateDir(), absRoot, report); perr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warn: persist report: %v\n", perr)
		}
		consecutiveFails = updateFailCounter(consecutiveFails, report)
		if consecutiveFails >= 10 {
			code := giveUpExitCode(report)
			return false, newCliError(code, "watch giving up after 10 consecutive all-fail cycles", "")
		}
		return true, nil
	}

	ok, err := runCycle()
	if !ok {
		return err
	}

	ticker := time.NewTicker(opts.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			ok, err := runCycle()
			if !ok {
				return err
			}
		}
	}
}

func scanAndUpload(ctx context.Context, cmd *cobra.Command, c *httpClient, opts watchOptions, absRoot string, cursors map[string]watchCursor, pending map[string]*pendingFile, sourceMetadata *apiclient.FileSourceMetadata) cycleReport {
	report := cycleReport{Timestamp: time.Now().UTC().Format(time.RFC3339)}
	seen := make(map[string]bool)

	_ = filepath.WalkDir(absRoot, func(p string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		abs, absErr := filepath.Abs(p)
		if absErr != nil {
			return nil
		}
		seen[abs] = true
		report.Counts.Scanned++

		fi, infoErr := d.Info()
		if infoErr != nil {
			report.Counts.Failed++
			report.Items = append(report.Items, cycleItem{Status: "failed", LocalPath: abs, FailureCode: failureReadDenied})
			return nil
		}

		cur, hasCursor := cursors[abs]
		sizeStr := fmt.Sprintf("%d", fi.Size())
		mtimeStr := fi.ModTime().UTC().Format("2006-01-02T15:04:05Z")

		if !hasCursor {
			prev, hasPending := pending[abs]
			if !hasPending || prev.Size != fi.Size() || prev.ModTime != mtimeStr {
				pending[abs] = &pendingFile{Abs: abs, Size: fi.Size(), ModTime: mtimeStr}
				return nil
			}
			delete(pending, abs)
			uploadOne(cmd, c, opts, abs, fi, &report, cursors, sourceMetadata)
			return nil
		}

		if sizeStr == cur.ModTime || (fi.Size() == cur.Size && mtimeStr == cur.ModTime) {
			report.Counts.Unchanged++
			report.Items = append(report.Items, cycleItem{Status: "unchanged", LocalPath: abs, FileID: cur.FileID})
			return nil
		}

		hash, hashErr := computeSHA256(abs)
		if hashErr != nil {
			report.Counts.Failed++
			report.Items = append(report.Items, cycleItem{Status: "failed", LocalPath: abs, FailureCode: failureReadDenied})
			return nil
		}
		if hash == cur.SHA256 {
			cur.Size = fi.Size()
			cur.ModTime = mtimeStr
			cursors[abs] = cur
			_ = saveWatchCursor(opts.cursorDir(), cur)
			report.Counts.Unchanged++
			report.Items = append(report.Items, cycleItem{Status: "unchanged", LocalPath: abs, FileID: cur.FileID})
			return nil
		}

		prefix := hash
		if len(prefix) > 16 {
			prefix = prefix[:16]
		}
		report.Items = append(report.Items, cycleItem{Status: "changed", LocalPath: abs, FileID: cur.FileID, SHA256Prefix: prefix})
		cur.SHA256 = hash
		cur.Size = fi.Size()
		cur.ModTime = mtimeStr
		cursors[abs] = cur
		_ = saveWatchCursor(opts.cursorDir(), cur)
		return nil
	})

	for abs, cur := range cursors {
		if seen[abs] {
			continue
		}
		if isPending(pending, abs) {
			continue
		}
		report.Counts.LocalGone++
		report.Items = append(report.Items, cycleItem{Status: "local_gone", LocalPath: abs, FileID: cur.FileID})
	}

	return report
}

func isPending(pending map[string]*pendingFile, abs string) bool {
	_, ok := pending[abs]
	return ok
}

func uploadOne(cmd *cobra.Command, c *httpClient, opts watchOptions, abs string, fi os.FileInfo, report *cycleReport, cursors map[string]watchCursor, sourceMetadata *apiclient.FileSourceMetadata) {
	rel, _ := filepath.Abs(abs)
	rootAbs, _ := filepath.Abs(opts.root)
	relPath, _ := filepath.Rel(rootAbs, rel)
	subFolder := opts.toFolder
	if d := filepath.Dir(relPath); d != "" && d != "." {
		subFolder = joinFolder(opts.toFolder, filepath.ToSlash(d))
	}

	name := filepath.Base(abs)
	mimeType := mime.TypeByExtension(filepath.Ext(name))

	f, err := os.Open(abs)
	if err != nil {
		report.Counts.Failed++
		report.Items = append(report.Items, cycleItem{Status: "failed", LocalPath: abs, FailureCode: failureReadDenied})
		return
	}
	defer f.Close()

	var resp map[string]any
	if err := c.api.UploadMultipartWithSourceMetadata(cmd.Context(), name, mimeType, subFolder, f, opts.tags, sourceMetadata, &resp); err != nil {
		report.Counts.Failed++
		code := classifyUploadError(err)
		report.Items = append(report.Items, cycleItem{Status: "failed", LocalPath: abs, FailureCode: code})
		fmt.Fprintf(cmd.ErrOrStderr(), "warn: upload %s: %v\n", abs, err)
		return
	}

	fileObj, _ := resp["file"].(map[string]any)
	fileID, _ := fileObj["id"].(string)
	virtualPath, _ := fileObj["path"].(string)
	if virtualPath == "" {
		if p, ok := fileObj["virtual_path"].(string); ok {
			virtualPath = p
		}
	}

	hash, _ := computeSHA256(abs)
	deduped, _ := resp["deduped"].(bool)

	status := "ingested"
	if deduped {
		status = "deduped"
		report.Counts.Deduped++
	} else {
		report.Counts.Ingested++
	}

	report.Items = append(report.Items, cycleItem{
		Status:      status,
		LocalPath:   abs,
		FileID:      fileID,
		VirtualPath: virtualPath,
	})

	cursors[abs] = watchCursor{
		Abs:        abs,
		Size:       fi.Size(),
		ModTime:    fi.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		SHA256:     hash,
		FileID:     fileID,
		IngestedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_ = saveWatchCursor(filepath.Join(cliStateRoot(), "watch", "cursors"), cursors[abs])
}

func computeSHA256(abs string) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func watchCursorPath(cursorDir, abs string) string {
	return filepath.Join(cursorDir, sha1Hex(abs)+".json")
}

func loadWatchCursor(cursorDir, abs string) (watchCursor, bool) {
	p := watchCursorPath(cursorDir, abs)
	b, err := os.ReadFile(p)
	if err != nil {
		return watchCursor{}, false
	}
	var cur watchCursor
	if err := json.Unmarshal(b, &cur); err != nil {
		return watchCursor{}, false
	}
	return cur, true
}

func saveWatchCursor(cursorDir string, cur watchCursor) error {
	p := watchCursorPath(cursorDir, cur.Abs)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".cursor-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

func loadAllWatchCursors(cursorDir string) map[string]watchCursor {
	cursors := make(map[string]watchCursor)
	entries, err := os.ReadDir(cursorDir)
	if err != nil {
		return cursors
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cursorDir, e.Name()))
		if err != nil {
			continue
		}
		var cur watchCursor
		if err := json.Unmarshal(b, &cur); err != nil {
			continue
		}
		if cur.Abs != "" {
			cursors[cur.Abs] = cur
		}
	}
	return cursors
}
