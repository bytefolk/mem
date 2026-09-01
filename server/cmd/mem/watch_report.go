package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
	"github.com/spf13/cobra"
)

const (
	failureAuth            = "auth"
	failurePlanQuota       = "plan_quota"
	failureProviderTimeout = "provider_timeout"
	failureNetwork         = "network"
	failureReadDenied      = "read_denied"
	failureUploadRejected  = "upload_rejected"
	failureRootMissing     = "root_missing"
	failureStateCorrupt    = "state_corrupt"
)

const maxReportLines = 200

type cycleReport struct {
	Timestamp string      `json:"timestamp"`
	Counts    cycleCounts `json:"counts"`
	Items     []cycleItem `json:"items"`
}

type cycleCounts struct {
	Scanned   int `json:"scanned"`
	Ingested  int `json:"ingested"`
	Deduped   int `json:"deduped"`
	Unchanged int `json:"unchanged"`
	LocalGone int `json:"local_gone"`
	Failed    int `json:"failed"`
}

type cycleItem struct {
	Status       string `json:"status"`
	LocalPath    string `json:"local_path,omitempty"`
	FileID       string `json:"file_id,omitempty"`
	VirtualPath  string `json:"virtual_path,omitempty"`
	SHA256Prefix string `json:"sha256_prefix,omitempty"`
	FailureCode  string `json:"failure_code,omitempty"`
}

func sha1Hex(s string) string {
	sum := sha1.Sum([]byte(s))
	return fmt.Sprintf("%x", sum[:])
}

func reportPath(stateDir, absRoot string) string {
	return filepath.Join(stateDir, "reports", sha1Hex(absRoot)+".jsonl")
}

func persistReport(stateDir, absRoot string, report cycleReport) error {
	p := reportPath(stateDir, absRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create report dir: %w", err)
	}

	b, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}

	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open report: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("append report: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close report: %w", err)
	}

	return capReportLines(p, maxReportLines)
}

func capReportLines(path string, max int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	_ = f.Close()
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(lines) <= max {
		return nil
	}
	lines = lines[len(lines)-max:]
	tmp, err := os.CreateTemp(filepath.Dir(path), ".report-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	for _, line := range lines {
		if _, err := tmp.WriteString(line + "\n"); err != nil {
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

func printReport(cmd *cobra.Command, report cycleReport, format string) {
	out := cmd.OutOrStdout()
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
		return
	}
	c := report.Counts
	fmt.Fprintf(out, "watch cycle %s: scanned=%d ingested=%d deduped=%d unchanged=%d local_gone=%d failed=%d\n",
		report.Timestamp, c.Scanned, c.Ingested, c.Deduped, c.Unchanged, c.LocalGone, c.Failed)
	for _, item := range report.Items {
		switch item.Status {
		case "ingested", "deduped":
			fmt.Fprintf(out, "  %s %s -> %s (id=%s)\n", item.Status, item.LocalPath, item.VirtualPath, item.FileID)
		case "changed":
			fmt.Fprintf(out, "  %s %s (sha256=%s...)\n", item.Status, item.LocalPath, item.SHA256Prefix)
		case "local_gone":
			fmt.Fprintf(out, "  %s %s (was id=%s)\n", item.Status, item.LocalPath, item.FileID)
		case "failed":
			fmt.Fprintf(out, "  %s %s [%s]\n", item.Status, item.LocalPath, item.FailureCode)
		}
	}
}

func classifyUploadError(err error) string {
	var ae *apiclient.APIError
	if !errors.As(err, &ae) {
		if isNetworkError(err) {
			return failureNetwork
		}
		return failureUploadRejected
	}
	switch ae.Kind() {
	case apiclient.KindAuth:
		return failureAuth
	case apiclient.KindPlan, apiclient.KindQuota:
		return failurePlanQuota
	case apiclient.KindProvider, apiclient.KindTimeout:
		return failureProviderTimeout
	default:
		return failureUploadRejected
	}
}

func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "network is unreachable")
}

func isNonRetryableFailure(code string) bool {
	switch code {
	case failureAuth, failurePlanQuota, failureUploadRejected:
		return true
	}
	return false
}

func updateFailCounter(prev int, report cycleReport) int {
	hasSuccess := report.Counts.Ingested > 0 || report.Counts.Deduped > 0 || report.Counts.Unchanged > 0
	if hasSuccess {
		return 0
	}
	if report.Counts.Failed == 0 {
		return prev
	}
	allNonRetryable := true
	for _, item := range report.Items {
		if item.Status == "failed" && !isNonRetryableFailure(item.FailureCode) {
			allNonRetryable = false
			break
		}
	}
	if allNonRetryable {
		return prev + 1
	}
	return 0
}

func giveUpExitCode(report cycleReport) int {
	for _, item := range report.Items {
		if item.Status != "failed" {
			continue
		}
		switch item.FailureCode {
		case failureAuth:
			return 3
		case failurePlanQuota:
			return 4
		case failureProviderTimeout:
			return 5
		}
	}
	return 1
}
