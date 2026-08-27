package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// qoderTurn is one normalized conversation turn extracted from a Qoder/CLI
// session transcript (.jsonl). It is CLI-schema-agnostic: the parser is
// tolerant of the many JSON shapes AI CLIs use and keeps only the fields that
// map onto mem's record model.
type qoderTurn struct {
	// Content is the message text. Empty turns are dropped.
	Content string
	// Role is the speaker role (e.g. "user", "assistant", "system") when the
	// transcript records it; otherwise empty.
	Role string
	// EventAt is the message timestamp when recorded; otherwise nil.
	EventAt *time.Time
	// AgentID is the model/agent identifier when recorded (e.g. "codex",
	// "claude-4-5"); otherwise empty.
	AgentID string
	// Line is the 1-based line number in the source file.
	Line int
}

// maxMessageBytes caps the content of a single message. mem truncates long
// model text at the write path, but we bound it here too so a pathological
// transcript cannot push unbounded data through the CLI.
const maxMessageBytes = 512 * 1024

// splitTranscriptPath returns the project name and a stable session slug
// derived from a transcript path relative to the ingest root. Given
//
//	root=/home/u/.qoder/projects, abs=.../projects/campus-2027/sessions/recruit-s3e0a.jsonl
//
// it yields project "campus-2027" and session "recruit-s3e0a". The project is
// the first path segment under root; the session is the file base name without
// its .jsonl extension. When the file is not under root, the project falls back
// to the immediate parent directory name.
func splitTranscriptPath(root, abs string) (project string, session string) {
	base := filepath.Base(abs)
	session = strings.TrimSuffix(base, filepath.Ext(base))
	if session == "" {
		session = base
	}

	if root != "" {
		if rel, err := filepath.Rel(root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			if i := strings.IndexByte(rel, os.PathSeparator); i > 0 {
				project = rel[:i]
			} else {
				project = rel
			}
		}
	}
	if project == "" || project == "." {
		project = filepath.Base(filepath.Dir(abs))
	}
	return project, session
}

// parseQoderTranscript reads a .jsonl transcript line by line and returns one
// qoderTurn per parseable message. Lines that do not decode as JSON, do not
// carry message text, or fall at or before the already-ingested line cursor
// (skipBefore, inclusive) are skipped. The returned turns are ordered by line.
func parseQoderTranscript(abs string, skipBefore int) ([]qoderTurn, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()
	return parseQoderTranscriptFrom(f, skipBefore)
}

// parseQoderTranscriptFrom is the io.Reader variant used by tests.
func parseQoderTranscriptFrom(r io.Reader, skipBefore int) ([]qoderTurn, error) {
	var turns []qoderTurn
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*maxMessageBytes)
	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || (skipBefore > 0 && line <= skipBefore) {
			continue
		}
		// A single logical record may span multiple physical lines. The common
		// AI-CLI shape is one JSON object per line, so we treat each line
		// independently but skip lines that are clearly a continuation of the
		// previous object (they do not parse as a complete object either way).
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			continue // not a standalone JSON object — skip (e.g. continuation)
		}
		turn := turnFromObject(obj)
		if turn == nil || turn.Content == "" {
			continue
		}
		turn.Line = line
		turns = append(turns, *turn)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	return turns, nil
}

// turnFromObject normalizes one decoded JSON line into a qoderTurn, probing the
// well-known key shapes used by AI CLI transcript stores. It returns nil when
// the object carries no message text worth ingesting.
func turnFromObject(obj map[string]any) *qoderTurn {
	t := &qoderTurn{}

	// ---- message text ------------------------------------------------------
	t.Content = firstString(obj,
		"content", "text", "message", "output", "body",
		"message.content", "message.text", "message.body",
		"data.content", "data.text", "payload.content",
	)
	if strings.TrimSpace(t.Content) == "" {
		return nil
	}
	if len(t.Content) > maxMessageBytes {
		t.Content = t.Content[:maxMessageBytes]
	}

	// ---- role / speaker ----------------------------------------------------
	t.Role = firstString(obj, "role", "type", "speaker", "message.role", "sender")
	if t.Role == "" {
		if _, ok := obj["user"]; ok {
			t.Role = "user"
		} else if _, ok := obj["assistant"]; ok {
			t.Role = "assistant"
		}
	}

	// ---- agent / model -----------------------------------------------------
	t.AgentID = firstString(obj,
		"model", "agent_id", "agentId", "agent", "model_id", "assistant.model",
	)

	// ---- timestamp ----------------------------------------------------------
	ts := firstTimeString(obj,
		"timestamp", "created_at", "createdAt", "ts", "time",
		"message.created_at", "message.timestamp", "data.created_at",
	)
	if t0 := parseTimeLike(ts); t0 != nil {
		t.EventAt = t0
	}
	return t
}

// firstTimeString is like firstString but also accepts numeric (epoch) values,
// which transcripts commonly use for timestamps with string keys.
func firstTimeString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := obj[key]; ok {
			switch x := v.(type) {
			case string:
				if x != "" {
					return x
				}
			case float64:
				return strconv.FormatFloat(x, 'f', -1, 64)
			case json.Number:
				return x.String()
			}
		}
	}
	return ""
}

// firstString returns the first non-empty string among the candidate keys. A
// candidate value that is a JSON string is used verbatim; a nested object is
// probed recursively. This keeps the parser tolerant of both flat
// ({"content":"..."}) and nested ({"message":{"content":"..."}}) shapes.
func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := obj[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			if x != "" {
				return x
			}
		case map[string]any:
			if s, ok := firstNestedString(x); ok {
				return s
			}
		case []any:
			if s, ok := firstListString(x); ok {
				return s
			}
		}
	}
	return ""
}

// firstNestedString digs for a content-bearing field inside a nested object
// (e.g. {"type":"text","text":"..."} in a parts array).
func firstNestedString(obj map[string]any) (string, bool) {
	for _, key := range []string{"text", "content", "value", "message", "body"} {
		if s, ok := obj[key]; ok {
			if str, ok := s.(string); ok && strings.TrimSpace(str) != "" {
				return str, true
			}
		}
	}
	return "", false
}

// firstListString joins text-bearing entries of a parts/content array.
func firstListString(arr []any) (string, bool) {
	var parts []string
	for _, item := range arr {
		switch x := item.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				parts = append(parts, x)
			}
		case map[string]any:
			if s, ok := firstNestedString(x); ok {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

// parseTimeLike flexibly parses the common timestamp spellings used in
// transcripts (RFC 3339 / ISO 8601 / epoch seconds/millis). It returns nil on
// any input it cannot parse rather than failing the whole ingest.
func parseTimeLike(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var result time.Time
	var err error
	switch {
	case strings.HasPrefix(raw, "202") && strings.Contains(raw, "T"):
		result, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			result, err = time.Parse("2006-01-02 15:04:05", raw)
		}
	case looksNumeric(raw):
		// epoch milliseconds (~1.7e12) or epoch seconds (~1.7e9).
		var f float64
		if _, e := fmt.Sscanf(raw, "%f", &f); e == nil {
			switch {
			case f > 1e11: // milliseconds
				result = time.Unix(int64(f/1000), (int64(f)%1000)*int64(time.Millisecond))
			case f > 1e8: // epoch seconds (2016+)
				sec := int64(f)
				ns := int64((f - float64(sec)) * 1e9)
				result = time.Unix(sec, ns)
			default:
				// too small to be a plausible epoch; treat as unparseable.
			}
		}
	}
	if err != nil {
		return nil
	}
	if result.IsZero() {
		return nil
	}
	return &result
}

func looksNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' && r != '-' && r != '+' {
			return false
		}
	}
	return true
}
