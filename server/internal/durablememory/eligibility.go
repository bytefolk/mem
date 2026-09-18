package durablememory

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// EvaluateRecall decides whether one record may be injected. Pin may preserve
// TTL eligibility; it never enlarges workspace, principal, scope, or grant.
func EvaluateRecall(rec Record, caller RecallCaller) Eligibility {
	if err := validateRecord(rec); err != nil {
		return Eligibility{OmitReason: OmitMalformed}
	}
	if !sameBinding(rec.Binding, caller) {
		return Eligibility{OmitReason: OmitOutOfScope}
	}
	at := caller.At
	if grantStatus(rec.Grant, at) == GrantStatusRevoked {
		return Eligibility{OmitReason: OmitRevoked}
	}
	switch rec.Lifecycle {
	case LifecycleForgotten:
		return Eligibility{OmitReason: OmitForgotten}
	case LifecycleArchived, LifecycleSuperseded:
		return Eligibility{OmitReason: OmitSuperseded}
	}
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(at) && !rec.Pinned {
		return Eligibility{OmitReason: OmitExpired}
	}
	if rec.Lifecycle == LifecycleExpired && !rec.Pinned {
		return Eligibility{OmitReason: OmitExpired}
	}
	return Eligibility{Eligible: true}
}

// PhysicalDeleteImplied reports whether TTL/expiry authorizes destroying the
// source log. It is always false: expiry is recall eligibility only.
func PhysicalDeleteImplied(rec Record, caller RecallCaller) bool {
	_ = rec
	_ = caller
	return false
}

// ExactReadback compares decoded envelope fields after the same validateRecord
// gate used on decode. It is not RFC 8785 JSON canonicalization.
func ExactReadback(stored, observed Record) error {
	if err := validateRecord(stored); err != nil {
		return fmt.Errorf("%w: stored: %v", ErrReadbackMismatch, err)
	}
	if err := validateRecord(observed); err != nil {
		return fmt.Errorf("%w: observed: %v", ErrReadbackMismatch, err)
	}
	if stored.MemoryID != observed.MemoryID ||
		stored.Kind != observed.Kind ||
		stored.Binding != observed.Binding ||
		stored.Grant.GrantID != observed.Grant.GrantID ||
		stored.Grant.Mode != observed.Grant.Mode ||
		stored.Grant.GrantVersion != observed.Grant.GrantVersion ||
		stored.Grant.PermissionDigest != observed.Grant.PermissionDigest ||
		stored.Source != observed.Source ||
		stored.Producer != observed.Producer ||
		stored.Text != observed.Text ||
		stored.Digest != observed.Digest ||
		stored.StateVersion != observed.StateVersion ||
		stored.Pinned != observed.Pinned ||
		stored.Lifecycle != observed.Lifecycle {
		return ErrReadbackMismatch
	}
	if len(stored.Citations) != len(observed.Citations) {
		return ErrReadbackMismatch
	}
	for i := range stored.Citations {
		if stored.Citations[i] != observed.Citations[i] {
			return ErrReadbackMismatch
		}
	}
	return nil
}

// EvaluateForget is permissioned. Failure is a visible error code. This
// contract evaluator never reports a local fake delete as success.
func EvaluateForget(rec Record, actor ForgetActor) ForgetDecision {
	denied := ForgetDecision{ErrorCode: ErrorForgetDenied}
	if err := validateRecord(rec); err != nil {
		return denied
	}
	if !sameBinding(rec.Binding, RecallCaller{
		WorkspaceID: actor.WorkspaceID,
		Principal:   actor.Principal,
		MemoryScope: actor.MemoryScope,
	}) {
		return denied
	}
	if !actor.WorkspaceRoleAllowsDelete || !hasScope(actor.TokenScopes, GrantModeDelete) {
		return denied
	}
	return ForgetDecision{Authorized: true}
}

// BuildReceipt projects grant/revocation into the readback envelope.
// Out-of-scope and malformed probes are indistinguishable from absence.
func BuildReceipt(rec Record, caller RecallCaller) RecallReceipt {
	elig := EvaluateRecall(rec, caller)
	if elig.OmitReason == OmitOutOfScope || elig.OmitReason == OmitMalformed {
		return RecallReceipt{Contract: ContractVersion, Eligible: false}
	}
	at := caller.At
	receipt := RecallReceipt{
		Contract:     ContractVersion,
		MemoryID:     rec.MemoryID,
		Locator:      locator(rec.MemoryID, rec.StateVersion),
		StateVersion: rec.StateVersion,
		Eligible:     elig.Eligible,
		OmitReason:   elig.OmitReason,
		Pinned:       rec.Pinned,
		Grant: ReceiptGrant{
			GrantID:          rec.Grant.GrantID,
			GrantVersion:     rec.Grant.GrantVersion,
			Mode:             rec.Grant.Mode,
			Status:           grantStatus(rec.Grant, at),
			PermissionDigest: rec.Grant.PermissionDigest,
			RevokedAt:        rec.Grant.RevokedAt,
		},
	}
	if elig.Eligible {
		receipt.Readback = cloneRecord(rec)
	}
	return receipt
}

func cloneRecord(rec Record) *Record {
	clone := rec
	if rec.ExpiresAt != nil {
		expires := *rec.ExpiresAt
		clone.ExpiresAt = &expires
	}
	if rec.Confidence != nil {
		conf := *rec.Confidence
		clone.Confidence = &conf
	}
	if rec.Grant.RevokedAt != nil {
		revoked := *rec.Grant.RevokedAt
		clone.Grant.RevokedAt = &revoked
	}
	if rec.Citations != nil {
		clone.Citations = append([]string(nil), rec.Citations...)
	}
	return &clone
}

func locator(memoryID uuid.UUID, stateVersion int64) string {
	return fmt.Sprintf("mem://memories/%s@%d", memoryID, stateVersion)
}

func sameBinding(b Binding, caller RecallCaller) bool {
	if b.WorkspaceID.String() != strings.TrimSpace(caller.WorkspaceID) {
		return false
	}
	if b.Principal != strings.TrimSpace(caller.Principal) {
		return false
	}
	return b.MemoryScope == strings.TrimSpace(caller.MemoryScope)
}

func hasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}
