// Package durablememory owns the additive durable-memory.v1 contract.
//
// This package is the version-pinned envelope for derived RoleWeave/mem
// records: principal binding, grant/revocation, expiry, pin, forget, and
// exact readback. It does not persist rows, expose HTTP, or replace the
// canonical /v1/memories control plane. Runtime wiring waits for Gate D0.
package durablememory

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	ContractVersion   = "durable-memory.v1"
	CapabilityGrantV1 = "capability-grant.v1"

	KindProjectDecision  = "project_decision"
	KindUserPreference   = "user_preference"
	KindReusableWorkflow = "reusable_workflow"
	KindNegativeSignal   = "negative_signal"
	KindActiveTaskState  = "active_task_state"
	KindComplianceRetain = "compliance_retained"
	KindObservation      = "observation"
	KindDecision         = "decision"
	KindPreference       = "preference"
	KindTaskState        = "task_state"
	KindFact             = "fact"
	KindNote             = "note"
	KindArtifact         = "artifact"

	LifecycleActive     = "active"
	LifecycleArchived   = "archived"
	LifecycleSuperseded = "superseded"
	LifecycleExpired    = "expired"
	LifecycleForgotten  = "forgotten"

	GrantModeRead   = "read"
	GrantModeWrite  = "write"
	GrantModeDelete = "delete"

	GrantStatusActive  = "active"
	GrantStatusRevoked = "revoked"

	TrustUntrusted = "untrusted"
	AuthorityNone  = "none"

	SourceKindSegment  = "segment"
	SourceKindMemory   = "memory"
	SourceKindArtifact = "artifact"

	OmitExpired    = "expired"
	OmitRevoked    = "revoked"
	OmitMalformed  = "malformed"
	OmitSuperseded = "superseded"
	OmitForgotten  = "forgotten"
	OmitOutOfScope = "out_of_scope"

	ErrorForgetDenied = "forget_denied"
)

var (
	ErrMalformed           = errors.New("durable memory record malformed")
	ErrReadbackMismatch    = errors.New("durable memory readback mismatch")
	ErrUnsupportedContract = errors.New("durable memory contract unsupported")
)

// Record is one derived durable-memory.v1 occurrence. Scope is not a free
// string: callers must bind workspace, position principal, memory scope, and
// a grant/revocation tuple.
type Record struct {
	Contract     string     `json:"contract"`
	MemoryID     uuid.UUID  `json:"memory_id"`
	Kind         string     `json:"kind"`
	Binding      Binding    `json:"binding"`
	Grant        Grant      `json:"grant"`
	Source       Source     `json:"source"`
	Citations    []string   `json:"citations"`
	Producer     Producer   `json:"producer"`
	EventAt      time.Time  `json:"event_at"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Importance   string     `json:"importance,omitempty"`
	Confidence   *float64   `json:"confidence,omitempty"`
	StateVersion int64      `json:"state_version"`
	Pinned       bool       `json:"pinned"`
	Lifecycle    string     `json:"lifecycle"`
	Trust        string     `json:"trust"`
	Authority    string     `json:"authority"`
	Text         string     `json:"text"`
	Digest       string     `json:"digest"`
}

// Binding is the fail-closed isolation key. Cross-principal default deny.
type Binding struct {
	WorkspaceID uuid.UUID `json:"workspace_id"`
	PositionID  string    `json:"position_id"`
	Principal   string    `json:"principal"`
	MemoryScope string    `json:"memory_scope"`
}

// Grant reuses mem durable-context grants and capability-grant.v1. Revocation
// is a first-class field so receipts can show why recall was denied.
type Grant struct {
	GrantID          uuid.UUID          `json:"grant_id"`
	Mode             string             `json:"mode"`
	GrantVersion     int64              `json:"grant_version"`
	PermissionDigest string             `json:"permission_digest"`
	CapabilityGrant  CapabilityGrantRef `json:"capability_grant"`
	GrantedAt        time.Time          `json:"granted_at"`
	RevokedAt        *time.Time         `json:"revoked_at"`
}

// CapabilityGrantRef is a normative pointer at capability-grant.v1, not a
// second authorization implementation.
type CapabilityGrantRef struct {
	SchemaVersion string `json:"schema_version"`
	Server        string `json:"server"`
}

// Source identifies the parent segment or artifact. Digests are full sha256.
type Source struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Producer is the writing Agent identity. It is evidence, not a permission.
type Producer struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
}

// RecallCaller is the principal asking to resume one record.
type RecallCaller struct {
	WorkspaceID string
	Principal   string
	MemoryScope string
	At          time.Time
}

// Eligibility is the recall decision. OmitReason is empty when Eligible.
type Eligibility struct {
	Eligible   bool
	OmitReason string
}

// ForgetActor is the permissioned caller of an explicit forget. A pin or
// read grant never authorizes this operation.
type ForgetActor struct {
	WorkspaceID               string
	Principal                 string
	MemoryScope               string
	TokenScopes               []string
	WorkspaceRoleAllowsDelete bool
}

// ForgetDecision is never a local fake delete. Authorized means the caller
// may invoke mem's permissioned forget API; Deleted stays false here.
type ForgetDecision struct {
	Authorized bool
	Deleted    bool
	LocalFake  bool
	ErrorCode  string
}

// ReceiptGrant is how grant and revocation enter readback/receipt.
type ReceiptGrant struct {
	GrantID          uuid.UUID  `json:"grant_id"`
	GrantVersion     int64      `json:"grant_version"`
	Mode             string     `json:"mode"`
	Status           string     `json:"status"`
	PermissionDigest string     `json:"permission_digest"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

// RecallReceipt is the exact-readback envelope for one record.
type RecallReceipt struct {
	Contract     string       `json:"contract"`
	MemoryID     uuid.UUID    `json:"memory_id"`
	Locator      string       `json:"locator"`
	StateVersion int64        `json:"state_version"`
	Eligible     bool         `json:"eligible"`
	OmitReason   string       `json:"omit_reason,omitempty"`
	Grant        ReceiptGrant `json:"grant"`
	Pinned       bool         `json:"pinned"`
	Readback     *Record      `json:"readback"`
}
