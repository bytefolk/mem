package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/apiclient"
	"github.com/PeterGuy326/mem/server/internal/tools"
)

func TestMemCheckpointSchemaContainsStrongNestedHandoffV1(t *testing.T) {
	reg := tools.New()
	if err := registerCheckpoint(reg, apiclient.New("http://unused", "token")); err != nil {
		t.Fatal(err)
	}
	tool, ok := reg.Get("mem_checkpoint")
	if !ok {
		t.Fatal("mem_checkpoint not registered")
	}
	handoff := tool.InputSchema.Properties["handoff"]
	if handoff.Const != nil || handoff.Type != "object" {
		t.Fatalf("handoff schema = %#v", handoff)
	}
	if handoff.Properties["contract"].Const != apiclient.HandoffContract ||
		handoff.Properties["schema_version"].Const != apiclient.HandoffSchemaVersion {
		t.Fatalf("handoff contract/version constraints missing: %#v", handoff.Properties)
	}
	state := handoff.Properties["state"]
	progress := state.Properties["progress"]
	completed := progress.Properties["completed"]
	if completed.Items == nil || completed.Items.Type != "string" {
		t.Fatalf("nested progress.completed schema = %#v", completed)
	}
	decisions := state.Properties["decisions"]
	if decisions.Items == nil ||
		decisions.Items.Properties["references"].Items == nil {
		t.Fatalf("nested decisions schema = %#v", decisions)
	}
	if handoff.AdditionalProperties == nil || *handoff.AdditionalProperties {
		t.Fatal("handoff must reject additional properties")
	}
}

func TestMemCheckpointUsesTypedClientContract(t *testing.T) {
	fs := newFakeServer(
		`{"checkpoint":{"id":"11111111-1111-1111-1111-111111111111"},"replayed":false}`,
		http.StatusCreated,
		"application/json",
	)
	defer fs.Close()
	reg := tools.New()
	if err := registerCheckpoint(reg, apiclient.New(fs.URL, "token")); err != nil {
		t.Fatal(err)
	}

	handoff := builtinValidHandoff("task-42")
	handoffMap := asObject(t, handoff)
	out, err := reg.Call(context.Background(), "mem_checkpoint", map[string]any{
		"task_key":        "task-42",
		"idempotency_key": "task-42-v1",
		"handoff":         handoffMap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if fs.lastMethod != http.MethodPost ||
		fs.lastPath != "/v1/tasks/task-42/checkpoints" {
		t.Fatalf("request = %s %s", fs.lastMethod, fs.lastPath)
	}
	if got := fs.lastHeaders.Get("Idempotency-Key"); got != "task-42-v1" {
		t.Fatalf("Idempotency-Key = %q", got)
	}
	var body map[string]any
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatal(err)
	}
	if body["contract"] != apiclient.HandoffContract ||
		body["task_key"] != "task-42" {
		t.Fatalf("checkpoint body = %#v", body)
	}
	if _, nestedWrapper := body["handoff"]; nestedWrapper {
		t.Fatalf("canonical API body must be the handoff itself: %#v", body)
	}
	raw, ok := out.(json.RawMessage)
	if !ok || !strings.Contains(string(raw), `"replayed":false`) {
		t.Fatalf("transparent output = %T %v", out, out)
	}
}

func TestMemResumeUsesTypedClientAndDoesNotDuplicateTaskKey(t *testing.T) {
	fs := newFakeServer(
		`{"contract":"mem.resume","schema_version":1,"missing":[]}`,
		http.StatusOK,
		"application/json",
	)
	defer fs.Close()
	reg := tools.New()
	if err := registerResume(reg, apiclient.New(fs.URL, "token")); err != nil {
		t.Fatal(err)
	}

	out, err := reg.Call(context.Background(), "mem_resume", map[string]any{
		"task_key":      "task-42",
		"checkpoint_id": "11111111-1111-1111-1111-111111111111",
		"scope":         "/Projects/mem",
		"focus":         "remaining tests",
		"limit":         float64(5),
		"max_chars":     float64(8000),
	})
	if err != nil {
		t.Fatal(err)
	}
	if fs.lastMethod != http.MethodPost ||
		fs.lastPath != "/v1/tasks/task-42/resume" {
		t.Fatalf("request = %s %s", fs.lastMethod, fs.lastPath)
	}
	var body map[string]any
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatal(err)
	}
	if _, duplicated := body["task_key"]; duplicated {
		t.Fatalf("task_key must only appear in URL: %#v", body)
	}
	if body["scope"] != "/Projects/mem" ||
		body["limit"] != float64(5) ||
		body["max_chars"] != float64(8000) {
		t.Fatalf("resume body = %#v", body)
	}
	raw, ok := out.(json.RawMessage)
	if !ok || !strings.Contains(string(raw), `"contract":"mem.resume"`) {
		t.Fatalf("transparent output = %T %v", out, out)
	}
}

func TestMemCheckpointRejectsUnknownNestedFields(t *testing.T) {
	fs := newFakeServer(`{}`, http.StatusCreated, "application/json")
	defer fs.Close()
	reg := tools.New()
	if err := registerCheckpoint(reg, apiclient.New(fs.URL, "token")); err != nil {
		t.Fatal(err)
	}
	handoff := asObject(t, builtinValidHandoff("task-42"))
	state := handoff["state"].(map[string]any)
	state["vendor_private_state"] = "must not cross the contract boundary"

	_, err := reg.Call(context.Background(), "mem_checkpoint", map[string]any{
		"task_key":        "task-42",
		"idempotency_key": "task-42-v1",
		"handoff":         handoff,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v", err)
	}
	if fs.lastMethod != "" {
		t.Fatalf("invalid input unexpectedly reached API: %s %s", fs.lastMethod, fs.lastPath)
	}
}

func builtinValidHandoff(taskKey string) apiclient.HandoffV1 {
	required := true
	return apiclient.HandoffV1{
		Contract:       apiclient.HandoffContract,
		SchemaVersion:  apiclient.HandoffSchemaVersion,
		CheckpointKind: "handoff",
		TaskKey:        taskKey,
		ScopePath:      "/Projects/mem",
		State: apiclient.HandoffState{
			Status: "ready",
			Goal:   "Continue the migration implementation",
			Progress: apiclient.HandoffProgress{
				Summary:   "MCP client surface implemented",
				Completed: []string{"Added recursive JSON Schema"},
			},
			Decisions: []apiclient.HandoffDecision{{
				Summary:    "Use typed apiclient methods",
				References: []string{},
			}},
			NextSteps: []apiclient.HandoffNextStep{{
				Summary:    "Run API integration tests",
				References: []string{},
			}},
			Blockers:      []apiclient.HandoffBlocker{},
			OpenQuestions: []string{},
			Artifacts: []apiclient.HandoffArtifact{{
				URI:      "mem://files/file-1",
				Required: &required,
			}},
		},
		Producer: apiclient.HandoffProducer{AgentID: "codex"},
	}
}

func asObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	return object
}
