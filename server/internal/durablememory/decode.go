package durablememory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PeterGuy326/mem/server/internal/pathx"
	"github.com/google/uuid"
)

const (
	maxTextRunes     = 16384
	maxCitationRunes = 2048
	maxCitations     = 32
	maxIDRunes       = 256
	maxScopeRunes    = 1024
)

var (
	principalRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	positionRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,118}$`)
	sha256RE      = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	memoryScopeRE = regexp.MustCompile(`^/workspaces/[^/]+/positions/[a-z0-9][a-z0-9._-]{0,118}$`)
	importanceSet = map[string]struct{}{"low": {}, "normal": {}, "high": {}}
	kindSet       = map[string]struct{}{
		KindProjectDecision:  {},
		KindUserPreference:   {},
		KindReusableWorkflow: {},
		KindNegativeSignal:   {},
		KindActiveTaskState:  {},
		KindComplianceRetain: {},
		KindObservation:      {},
		KindDecision:         {},
		KindPreference:       {},
		KindTaskState:        {},
		KindFact:             {},
		KindNote:             {},
		KindArtifact:         {},
	}
	lifecycleSet = map[string]struct{}{
		LifecycleActive:     {},
		LifecycleArchived:   {},
		LifecycleSuperseded: {},
		LifecycleExpired:    {},
		LifecycleForgotten:  {},
	}
	grantModeSet = map[string]struct{}{
		GrantModeRead: {},
	}
	sourceKindSet = map[string]struct{}{
		SourceKindSegment:  {},
		SourceKindMemory:   {},
		SourceKindArtifact: {},
	}
)

// DecodeRecord strictly decodes one durable-memory.v1 object. Unknown fields
// (including a free-string "scope") fail closed as malformed.
func DecodeRecord(raw []byte) (Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var rec Record
	if err := decoder.Decode(&rec); err != nil {
		return Record{}, malformed("decode: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Record{}, malformed("must contain exactly one JSON value")
		}
		return Record{}, malformed("decode: %v", err)
	}
	if err := validateRecord(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func validateRecord(rec Record) error {
	if rec.Contract != ContractVersion {
		if rec.Contract == "" {
			return malformed("contract is required")
		}
		return fmt.Errorf("%w: %s", ErrUnsupportedContract, rec.Contract)
	}
	if rec.MemoryID == uuid.Nil {
		return malformed("memory_id is required")
	}
	if _, ok := kindSet[rec.Kind]; !ok {
		return malformed("kind is not a durable-memory.v1 kind")
	}
	if err := validateBinding(rec.Binding); err != nil {
		return err
	}
	if err := validateGrant(rec.Binding, rec.Grant); err != nil {
		return err
	}
	if _, ok := sourceKindSet[rec.Source.Kind]; !ok {
		return malformed("source.kind is invalid")
	}
	if strings.TrimSpace(rec.Source.ID) == "" || utf8.RuneCountInString(rec.Source.ID) > maxIDRunes {
		return malformed("source.id is required")
	}
	if !sha256RE.MatchString(rec.Source.Digest) {
		return malformed("source.digest must be sha256:<64 hex>")
	}
	if rec.Citations == nil {
		return malformed("citations is required")
	}
	if len(rec.Citations) > maxCitations {
		return malformed("too many citations")
	}
	for _, citation := range rec.Citations {
		if strings.TrimSpace(citation) == "" || utf8.RuneCountInString(citation) > maxCitationRunes {
			return malformed("citation is invalid")
		}
	}
	if !principalRE.MatchString(strings.TrimSpace(rec.Producer.AgentID)) {
		return malformed("producer.agent_id must be a principal")
	}
	if rec.CreatedAt.IsZero() || rec.EventAt.IsZero() {
		return malformed("event_at and created_at are required")
	}
	if rec.ExpiresAt != nil && rec.ExpiresAt.IsZero() {
		return malformed("expires_at must be a real timestamp when set")
	}
	if rec.StateVersion < 1 {
		return malformed("state_version must be >= 1")
	}
	if _, ok := lifecycleSet[rec.Lifecycle]; !ok {
		return malformed("lifecycle is invalid")
	}
	if rec.Trust != TrustUntrusted {
		return malformed("trust must be untrusted")
	}
	if rec.Authority != AuthorityNone {
		return malformed("authority must be none")
	}
	if rec.Lifecycle == LifecycleForgotten {
		if rec.Text != "" {
			return malformed("forgotten records must redact text")
		}
	} else if strings.TrimSpace(rec.Text) == "" || utf8.RuneCountInString(rec.Text) > maxTextRunes {
		return malformed("text is required")
	}
	if rec.Digest != ContentDigest(rec.Text) {
		return malformed("digest must be sha256 of text UTF-8 bytes")
	}
	if rec.Importance != "" {
		if _, ok := importanceSet[rec.Importance]; !ok {
			return malformed("importance is invalid")
		}
	}
	if rec.Confidence != nil && (*rec.Confidence < 0 || *rec.Confidence > 1) {
		return malformed("confidence must be in [0,1]")
	}
	return nil
}

func validateBinding(b Binding) error {
	if b.WorkspaceID == uuid.Nil {
		return malformed("binding.workspace_id is required")
	}
	if !positionRE.MatchString(b.PositionID) {
		return malformed("binding.position_id is invalid")
	}
	if !principalRE.MatchString(b.Principal) {
		return malformed("binding.principal is invalid")
	}
	if b.Principal != "position."+b.PositionID {
		return malformed("binding.principal must equal position.<position_id>")
	}
	scope := strings.TrimSpace(b.MemoryScope)
	if scope == "" || scope == pathx.Root {
		return malformed("binding.memory_scope must be a non-root virtual path")
	}
	normalized, err := pathx.Normalize(scope)
	if err != nil {
		return malformed("binding.memory_scope: %v", err)
	}
	if normalized != scope {
		return malformed("binding.memory_scope must already be canonical")
	}
	if utf8.RuneCountInString(scope) > maxScopeRunes {
		return malformed("binding.memory_scope exceeds %d characters", maxScopeRunes)
	}
	if !memoryScopeRE.MatchString(scope) || !strings.HasSuffix(scope, "/positions/"+b.PositionID) {
		return malformed("binding.memory_scope must bind /workspaces/<id>/positions/<position_id>")
	}
	return nil
}

func validateGrant(b Binding, g Grant) error {
	if g.GrantID == uuid.Nil {
		return malformed("grant.grant_id is required")
	}
	if _, ok := grantModeSet[g.Mode]; !ok {
		return malformed("grant.mode is invalid")
	}
	if g.GrantVersion < 1 {
		return malformed("grant.grant_version must be >= 1")
	}
	want := PermissionDigest(b, g.Mode, g.GrantVersion)
	if g.PermissionDigest != want {
		return malformed("grant.permission_digest must bind workspace, principal, memory_scope, mode, and grant_version")
	}
	if g.CapabilityGrant.SchemaVersion != CapabilityGrantV1 {
		return malformed("grant.capability_grant.schema_version must be capability-grant.v1")
	}
	if g.CapabilityGrant.Server != "mem" {
		return malformed("grant.capability_grant.server must be mem")
	}
	if g.GrantedAt.IsZero() {
		return malformed("grant.granted_at is required")
	}
	if g.RevokedAt != nil && g.RevokedAt.Before(g.GrantedAt) {
		return malformed("grant.revoked_at cannot precede granted_at")
	}
	return nil
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, args...))
}

// ContentDigest is SHA-256 of the envelope text (UTF-8). Forgotten
// tombstones digest the empty string.
func ContentDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PermissionDigest is the SHA-256 of the canonical grant tuple. It is what
// receipts compare when detecting grant drift.
func PermissionDigest(b Binding, mode string, grantVersion int64) string {
	payload := fmt.Sprintf(
		"durable-memory.v1/permission\nworkspace_id=%s\nprincipal=%s\nmemory_scope=%s\nmode=%s\ngrant_version=%d\n",
		b.WorkspaceID, b.Principal, b.MemoryScope, mode, grantVersion,
	)
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func grantStatus(g Grant, at time.Time) string {
	if g.RevokedAt != nil && !g.RevokedAt.After(at) {
		return GrantStatusRevoked
	}
	return GrantStatusActive
}
