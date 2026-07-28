package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
)

func TestCheckpointCommandReadsStdinAndPostsPortableContract(t *testing.T) {
	taskKey := "team/release α"
	input := testHandoffJSON(t, taskKey)
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		wantPath := "/v1/tasks/" + url.PathEscape(taskKey) + "/checkpoints"
		if got := r.URL.EscapedPath(); got != wantPath {
			t.Errorf("escaped path = %q, want %q", got, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer checkpoint-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-checkpoint" {
			t.Errorf("X-Workspace-ID = %q", got)
		}
		if got := r.Header.Get("Idempotency-Key"); got != "release-checkpoint-1" {
			t.Errorf("Idempotency-Key = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}

		var document apiclient.HandoffV1
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&document); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if document.TaskKey != taskKey ||
			document.Contract != apiclient.HandoffContract ||
			document.Producer.AgentID != "claude-code" {
			t.Errorf("document = %+v", document)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"checkpoint":{"id":"checkpoint-1","task_key":"team/release α","sequence":1},
			"replayed":false
		}`)
	}))
	defer server.Close()

	setHandoffTestConfig(t, server.URL, "checkpoint-token", "workspace-checkpoint")
	root := newRootCmd()
	root.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{
		"checkpoint",
		"--input", "-",
		"--idempotency-key", "release-checkpoint-1",
		"--format", "json",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("request count = %d", requestCount.Load())
	}
	var output map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	checkpoint, _ := output["checkpoint"].(map[string]any)
	if checkpoint["id"] != "checkpoint-1" || output["replayed"] != false {
		t.Fatalf("stdout response = %#v", output)
	}
}

func TestCheckpointCommandReadsFile(t *testing.T) {
	taskKey := "file-input-task"
	inputPath := filepath.Join(t.TempDir(), "handoff.json")
	if err := os.WriteFile(inputPath, []byte(testHandoffJSON(t, taskKey)), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var document apiclient.HandoffV1
		if err := json.NewDecoder(r.Body).Decode(&document); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if document.TaskKey != taskKey {
			t.Errorf("task_key = %q", document.TaskKey)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{
			"checkpoint":{
				"id":"checkpoint-from-file",
				"task_key":"file-input-task",
				"sequence":2,
				"scope_path":"/Projects/mem"
			},
			"replayed":true
		}`)
	}))
	defer server.Close()

	setHandoffTestConfig(t, server.URL, "file-token", "file-workspace")
	root := newRootCmd()
	root.SetIn(strings.NewReader(`{"this":"must not be read"}`))
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{
		"checkpoint",
		"--input", inputPath,
		"--idempotency-key", "file-checkpoint-2",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"checkpoint: checkpoint-from-file",
		"task: file-input-task",
		"sequence: 2",
		"scope: /Projects/mem",
		"replayed: true",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestCheckpointCommandStrictlyRejectsInvalidJSONBeforeRequest(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	setHandoffTestConfig(t, server.URL, "token", "workspace")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "unknown field",
			input: strings.TrimSuffix(testHandoffJSON(t, "strict-task"), "}") + `,"unknown":true}`,
			want:  "unknown field",
		},
		{
			name:  "second document",
			input: testHandoffJSON(t, "strict-task") + "\n{}",
			want:  "exactly one JSON document",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newRootCmd()
			root.SetIn(strings.NewReader(test.input))
			root.SetArgs([]string{
				"checkpoint",
				"--input", "-",
				"--idempotency-key", "strict-checkpoint",
			})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
	if requestCount.Load() != 0 {
		t.Fatalf("invalid input made %d request(s)", requestCount.Load())
	}
}

func TestResumeCommandPostsSelectionAndBudgets(t *testing.T) {
	taskKey := "project/migration β"
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		wantPath := "/v1/tasks/" + url.PathEscape(taskKey) + "/resume"
		if got := r.URL.EscapedPath(); got != wantPath {
			t.Errorf("escaped path = %q, want %q", got, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer resume-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-resume" {
			t.Errorf("X-Workspace-ID = %q", got)
		}

		var request apiclient.ResumeRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.CheckpointID != "4ca7-checkpoint" ||
			request.Scope != "/Projects/mem" ||
			request.Focus != "unfinished import work" ||
			request.Limit != 7 ||
			request.MaxChars != 6000 {
			t.Errorf("request = %+v", request)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"contract":"mem.resume",
			"schema_version":1,
			"task":{"task_key":"project/migration β"},
			"checkpoint":{"id":"checkpoint-resume","sequence":3},
			"resolved":[],
			"missing":[],
			"complete":true
		}`)
	}))
	defer server.Close()

	setHandoffTestConfig(t, server.URL, "resume-token", "workspace-resume")
	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{
		"resume", taskKey,
		"--checkpoint-id", "4ca7-checkpoint",
		"--scope", "/Projects/mem",
		"--focus", "unfinished import work",
		"--limit", "7",
		"--max-chars", "6000",
		"--format", "json",
	})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if requestCount.Load() != 1 {
		t.Fatalf("request count = %d", requestCount.Load())
	}
	var output map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if output["contract"] != "mem.resume" || output["complete"] != true {
		t.Fatalf("stdout response = %#v", output)
	}
}

func testHandoffJSON(t *testing.T, taskKey string) string {
	t.Helper()
	document := apiclient.HandoffV1{
		Contract:       apiclient.HandoffContract,
		SchemaVersion:  apiclient.HandoffSchemaVersion,
		CheckpointKind: "handoff",
		TaskKey:        taskKey,
		ScopePath:      "/Projects/mem",
		State: apiclient.HandoffState{
			Status: "in_progress",
			Goal:   "Move work between Agents without losing context",
			Progress: apiclient.HandoffProgress{
				Summary:   "Portable checkpoint contract is ready",
				Completed: []string{"Defined v1"},
			},
			Decisions: []apiclient.HandoffDecision{{
				Summary:    "Use a vendor-neutral contract",
				References: []string{},
			}},
			NextSteps: []apiclient.HandoffNextStep{{
				Summary:    "Resume from another Agent",
				References: []string{},
			}},
			Blockers:      []apiclient.HandoffBlocker{},
			OpenQuestions: []string{},
			Artifacts:     []apiclient.HandoffArtifact{},
		},
		Producer: apiclient.HandoffProducer{
			AgentID:   "claude-code",
			SessionID: "session-1",
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func setHandoffTestConfig(t *testing.T, server, token, workspace string) {
	t.Helper()
	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server)
	t.Setenv("MEM_TOKEN", token)
	t.Setenv("MEM_WORKSPACE", workspace)
}
