package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	// HandoffContract and HandoffSchemaVersion identify the portable handoff
	// contract. They mirror docs/schemas/handoff.v1.schema.json.
	HandoffContract      = "mem.handoff"
	HandoffSchemaVersion = 1
)

// HandoffV1 is the vendor-neutral payload persisted by a checkpoint.
//
// TaskKey is deliberately present in the payload as well as the canonical API
// path. Client.Checkpoint rejects mismatches before any request is sent.
type HandoffV1 struct {
	Contract         string          `json:"contract"`
	SchemaVersion    int             `json:"schema_version"`
	CheckpointKind   string          `json:"checkpoint_kind"`
	TaskKey          string          `json:"task_key"`
	BaseCheckpointID *string         `json:"base_checkpoint_id,omitempty"`
	ScopePath        string          `json:"scope_path"`
	State            HandoffState    `json:"state"`
	Producer         HandoffProducer `json:"producer"`
}

type HandoffState struct {
	Status         string                 `json:"status"`
	Goal           string                 `json:"goal"`
	Progress       HandoffProgress        `json:"progress"`
	Decisions      []HandoffDecision      `json:"decisions"`
	NextSteps      []HandoffNextStep      `json:"next_steps"`
	Blockers       []HandoffBlocker       `json:"blockers"`
	OpenQuestions  []string               `json:"open_questions"`
	Artifacts      []HandoffArtifact      `json:"artifacts"`
	WorkspaceState *HandoffWorkspaceState `json:"workspace_state,omitempty"`
}

type HandoffProgress struct {
	Summary   string   `json:"summary"`
	Completed []string `json:"completed"`
}

type HandoffDecision struct {
	Summary    string   `json:"summary"`
	Rationale  string   `json:"rationale,omitempty"`
	References []string `json:"references"`
}

type HandoffNextStep struct {
	Summary    string   `json:"summary"`
	References []string `json:"references"`
}

type HandoffBlocker struct {
	Summary    string   `json:"summary"`
	Needs      string   `json:"needs,omitempty"`
	References []string `json:"references"`
}

type HandoffArtifact struct {
	URI      string `json:"uri"`
	Role     string `json:"role,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Required *bool  `json:"required"`
}

type HandoffWorkspaceState struct {
	WorkingDirectory string      `json:"working_directory,omitempty"`
	VCS              *HandoffVCS `json:"vcs,omitempty"`
}

type HandoffVCS struct {
	Revision      string `json:"revision,omitempty"`
	Branch        string `json:"branch,omitempty"`
	Dirty         *bool  `json:"dirty,omitempty"`
	StatusSummary string `json:"status_summary,omitempty"`
}

type HandoffProducer struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id,omitempty"`
}

// ResumeRequest selects an explicit checkpoint or the current task head and
// optionally narrows/augments the evidence returned with it. TaskKey belongs
// to the URL and is therefore a separate argument to Client.Resume.
type ResumeRequest struct {
	CheckpointID string `json:"checkpoint_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Focus        string `json:"focus,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	MaxChars     int    `json:"max_chars,omitempty"`
}

// Checkpoint idempotently persists one immutable handoff revision. The raw
// response is returned unchanged so adapters do not create a second response
// contract while the canonical API envelope evolves.
func (c *Client) Checkpoint(
	ctx context.Context,
	taskKey string,
	handoff HandoffV1,
	idempotencyKey string,
) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("apiclient: nil client")
	}
	if err := validateTaskKey(taskKey); err != nil {
		return nil, err
	}
	if taskKey != handoff.TaskKey {
		return nil, fmt.Errorf(
			"apiclient: URL task_key %q does not match handoff task_key %q",
			taskKey,
			handoff.TaskKey,
		)
	}
	if err := handoff.Validate(); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, fmt.Errorf("apiclient: idempotency key is required")
	}

	var out json.RawMessage
	err := c.DoJSONWithHeaders(
		ctx,
		http.MethodPost,
		taskEndpoint(taskKey, "checkpoints"),
		handoff,
		&out,
		map[string]string{"Idempotency-Key": idempotencyKey},
	)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Resume returns the canonical API response without reshaping it.
func (c *Client) Resume(
	ctx context.Context,
	taskKey string,
	req ResumeRequest,
) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("apiclient: nil client")
	}
	if err := validateTaskKey(taskKey); err != nil {
		return nil, err
	}
	if req.Scope != "" && !strings.HasPrefix(req.Scope, "/") {
		return nil, fmt.Errorf("apiclient: resume scope must be an absolute virtual path")
	}
	if req.Limit < 0 {
		return nil, fmt.Errorf("apiclient: resume limit must not be negative")
	}
	if req.MaxChars < 0 {
		return nil, fmt.Errorf("apiclient: resume max_chars must not be negative")
	}

	var out json.RawMessage
	if err := c.DoJSON(
		ctx,
		http.MethodPost,
		taskEndpoint(taskKey, "resume"),
		req,
		&out,
	); err != nil {
		return nil, err
	}
	return out, nil
}

// Validate checks the contract invariants adapters need in order to produce a
// portable handoff. The server remains authoritative for full size, UUID,
// checksum, and path validation.
func (h HandoffV1) Validate() error {
	if h.Contract != HandoffContract {
		return fmt.Errorf("apiclient: handoff contract must be %q", HandoffContract)
	}
	if h.SchemaVersion != HandoffSchemaVersion {
		return fmt.Errorf("apiclient: handoff schema_version must be %d", HandoffSchemaVersion)
	}
	switch h.CheckpointKind {
	case "checkpoint", "handoff":
	default:
		return fmt.Errorf("apiclient: checkpoint_kind must be checkpoint or handoff")
	}
	if err := validateTaskKey(h.TaskKey); err != nil {
		return err
	}
	if h.BaseCheckpointID != nil && strings.TrimSpace(*h.BaseCheckpointID) == "" {
		return fmt.Errorf("apiclient: base_checkpoint_id must not be empty when present")
	}
	if !strings.HasPrefix(h.ScopePath, "/") {
		return fmt.Errorf("apiclient: scope_path must be an absolute virtual path")
	}
	switch h.State.Status {
	case "in_progress", "ready", "blocked", "complete":
	default:
		return fmt.Errorf("apiclient: handoff state.status is invalid")
	}
	if strings.TrimSpace(h.State.Goal) == "" {
		return fmt.Errorf("apiclient: handoff state.goal is required")
	}
	if strings.TrimSpace(h.State.Progress.Summary) == "" {
		return fmt.Errorf("apiclient: handoff state.progress.summary is required")
	}
	if h.State.Progress.Completed == nil ||
		h.State.Decisions == nil ||
		h.State.NextSteps == nil ||
		h.State.Blockers == nil ||
		h.State.OpenQuestions == nil ||
		h.State.Artifacts == nil {
		return fmt.Errorf("apiclient: required handoff arrays must be present")
	}
	for i, decision := range h.State.Decisions {
		if strings.TrimSpace(decision.Summary) == "" || decision.References == nil {
			return fmt.Errorf("apiclient: handoff decision %d requires summary and references", i)
		}
	}
	for i, step := range h.State.NextSteps {
		if strings.TrimSpace(step.Summary) == "" || step.References == nil {
			return fmt.Errorf("apiclient: handoff next_step %d requires summary and references", i)
		}
	}
	for i, blocker := range h.State.Blockers {
		if strings.TrimSpace(blocker.Summary) == "" || blocker.References == nil {
			return fmt.Errorf("apiclient: handoff blocker %d requires summary and references", i)
		}
	}
	for i, artifact := range h.State.Artifacts {
		if strings.TrimSpace(artifact.URI) == "" {
			return fmt.Errorf("apiclient: handoff artifact %d requires uri", i)
		}
		if artifact.Required == nil {
			return fmt.Errorf("apiclient: handoff artifact %d requires required", i)
		}
	}
	if strings.TrimSpace(h.Producer.AgentID) == "" {
		return fmt.Errorf("apiclient: handoff producer.agent_id is required")
	}
	return nil
}

func validateTaskKey(taskKey string) error {
	if strings.TrimSpace(taskKey) == "" {
		return fmt.Errorf("apiclient: task_key is required")
	}
	if !utf8.ValidString(taskKey) {
		return fmt.Errorf("apiclient: task_key must be valid UTF-8")
	}
	if utf8.RuneCountInString(taskKey) > 200 {
		return fmt.Errorf("apiclient: task_key exceeds 200 characters")
	}
	return nil
}

func taskEndpoint(taskKey, action string) string {
	return "/v1/tasks/" + url.PathEscape(taskKey) + "/" + action
}
