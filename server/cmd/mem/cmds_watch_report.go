package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func writeWatchReport(w io.Writer, format string, report watchReport) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		return enc.Encode(report)
	}
	_, err := fmt.Fprintf(w,
		"watch scanned=%d ingested=%d deduped=%d unchanged=%d changed=%d local_gone=%d failed=%d\n",
		report.Scanned, report.Ingested, report.Deduped, report.Unchanged,
		report.Changed, report.LocalGone, report.Failed)
	return err
}

func appendWatchReport(path string, report watchReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create watch report dir: %w", err)
	}
	line, err := json.Marshal(report)
	if err != nil {
		return err
	}

	existing, err := readJSONL(path)
	if err != nil {
		return err
	}
	row := append(append([]byte(nil), line...), '\n')
	existing = append(existing, row)
	if len(existing) > watchReportCap {
		existing = existing[len(existing)-watchReportCap:]
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".watch-report-*.tmp")
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
	for _, row := range existing {
		if _, err := tmp.Write(row); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func readJSONL(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		row := append([]byte(nil), sc.Bytes()...)
		lines = append(lines, append(row, '\n'))
	}
	return lines, sc.Err()
}
