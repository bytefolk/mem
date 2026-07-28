package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckpointPostsVersionedHandoffAndIdempotencyHeader(t *testing.T) {
	var (
		gotMethod    string
		gotPath      string
		gotKey       string
		gotWorkspace string
		gotBody      []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		gotKey = r.Header.Get("Idempotency-Key")
		gotWorkspace = r.Header.Get("X-Workspace-ID")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"checkpoint":{"id":"cp-1"},"replayed":false}`)
	}))
	defer srv.Close()

	taskKey := "task 42/phase-a"
	response, err := New(srv.URL, "token").WithWorkspace("workspace-1").Checkpoint(
		context.Background(),
		taskKey,
		validHandoff(taskKey),
		"task-42-phase-a-v1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotPath != "/v1/tasks/task%2042%2Fphase-a/checkpoints" {
		t.Fatalf("escaped path = %q", gotPath)
	}
	if gotKey != "task-42-phase-a-v1" {
		t.Fatalf("Idempotency-Key = %q", gotKey)
	}
	if gotWorkspace != "workspace-1" {
		t.Fatalf("X-Workspace-ID = %q", gotWorkspace)
	}

	var body HandoffV1
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatal(err)
	}
	if body.Contract != HandoffContract ||
		body.SchemaVersion != HandoffSchemaVersion ||
		body.TaskKey != taskKey ||
		body.State.Progress.Completed == nil {
		t.Fatalf("handoff body = %#v", body)
	}
	if !strings.Contains(string(response), `"id":"cp-1"`) {
		t.Fatalf("transparent response = %s", response)
	}
}

func TestCheckpointRejectsURLPayloadTaskKeyMismatchBeforeRequest(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer srv.Close()

	handoff := validHandoff("payload-task")
	_, err := New(srv.URL, "token").Checkpoint(
		context.Background(),
		"url-task",
		handoff,
		"retry-1",
	)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("unexpected HTTP requests = %d", requests)
	}
}

func TestResumePostsSelectionWithoutDuplicatingTaskKeyInBody(t *testing.T) {
	var (
		gotPath string
		gotBody map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"contract":"mem.resume","schema_version":1,"missing":[]}`)
	}))
	defer srv.Close()

	response, err := New(srv.URL, "token").Resume(
		context.Background(),
		"task 42",
		ResumeRequest{
			CheckpointID: "11111111-1111-1111-1111-111111111111",
			Scope:        "/Projects/mem",
			Focus:        "remaining tests",
			Limit:        6,
			MaxChars:     9000,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/tasks/task%2042/resume" {
		t.Fatalf("escaped path = %q", gotPath)
	}
	if _, duplicated := gotBody["task_key"]; duplicated {
		t.Fatalf("task_key must only appear in the URL: %#v", gotBody)
	}
	if gotBody["scope"] != "/Projects/mem" ||
		gotBody["focus"] != "remaining tests" ||
		gotBody["limit"] != float64(6) ||
		gotBody["max_chars"] != float64(9000) {
		t.Fatalf("resume body = %#v", gotBody)
	}
	if !strings.Contains(string(response), `"contract":"mem.resume"`) {
		t.Fatalf("transparent response = %s", response)
	}
}

func TestHandoffValidateRequiresRequiredArrays(t *testing.T) {
	handoff := validHandoff("task-42")
	handoff.State.OpenQuestions = nil
	if err := handoff.Validate(); err == nil ||
		!strings.Contains(err.Error(), "arrays") {
		t.Fatalf("error = %v", err)
	}
}

func validHandoff(taskKey string) HandoffV1 {
	dirty := true
	required := true
	return HandoffV1{
		Contract:       HandoffContract,
		SchemaVersion:  HandoffSchemaVersion,
		CheckpointKind: "handoff",
		TaskKey:        taskKey,
		ScopePath:      "/Projects/mem",
		State: HandoffState{
			Status: "ready",
			Goal:   "Finish the portable Agent drive",
			Progress: HandoffProgress{
				Summary:   "Client contract implemented",
				Completed: []string{"Defined typed handoff DTO"},
			},
			Decisions: []HandoffDecision{{
				Summary:    "Use one canonical API",
				Rationale:  "Prevent adapter drift",
				References: []string{"mem://memories/decision-1"},
			}},
			NextSteps: []HandoffNextStep{{
				Summary:    "Run integration tests",
				References: []string{},
			}},
			Blockers:      []HandoffBlocker{},
			OpenQuestions: []string{},
			Artifacts: []HandoffArtifact{{
				URI:      "mem://files/file-1",
				Role:     "implementation",
				Required: &required,
			}},
			WorkspaceState: &HandoffWorkspaceState{
				WorkingDirectory: "/workspace/mem",
				VCS: &HandoffVCS{
					Revision: "abc123",
					Branch:   "codex/handoff",
					Dirty:    &dirty,
				},
			},
		},
		Producer: HandoffProducer{
			AgentID:   "claude-code",
			SessionID: "session-7",
		},
	}
}
