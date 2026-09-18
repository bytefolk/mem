package durablememory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// EvaluateRecall decides whether one record may be injected. Pin may preserve
// TTL eligibility; it never enlarges workspace, principal, scope, or grant.
func EvaluateRecall(rec Record, caller RecallCaller) Eligibility {
	if reason := malformedReason(rec); reason != "" {
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

// ExactReadback requires the observed record to match the stored canonical
// envelope byte-for-byte after JSON canonicalization.
func ExactReadback(stored, observed Record) error {
	want, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("%w: marshal stored: %v", ErrReadbackMismatch, err)
	}
	got, err := json.Marshal(observed)
	if err != nil {
		return fmt.Errorf("%w: marshal observed: %v", ErrReadbackMismatch, err)
	}
	if !bytes.Equal(want, got) {
		return ErrReadbackMismatch
	}
	return nil
}

// EvaluateForget is permissioned. Failure is a visible error code. This
// contract evaluator never reports a local fake delete as success.
func EvaluateForget(rec Record, actor ForgetActor) ForgetDecision {
	denied := ForgetDecision{ErrorCode: ErrorForgetDenied}
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
func BuildReceipt(rec Record, caller RecallCaller) RecallReceipt {
	at := caller.At
	elig := EvaluateRecall(rec, caller)
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
		clone := rec
		receipt.Readback = &clone
	}
	return receipt
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

func malformedReason(rec Record) string {
	if rec.Contract != ContractVersion {
		return OmitMalformed
	}
	if rec.MemoryID == uuid.Nil || rec.Binding.WorkspaceID == uuid.Nil || rec.Grant.GrantID == uuid.Nil {
		return OmitMalformed
	}
	if rec.Trust != TrustUntrusted || rec.Authority != AuthorityNone {
		return OmitMalformed
	}
	if rec.StateVersion < 1 || rec.Grant.GrantVersion < 1 {
		return OmitMalformed
	}
	if !sha256Digest(rec.Grant.PermissionDigest) || !sha256Digest(rec.Source.Digest) {
		return OmitMalformed
	}
	if rec.Lifecycle != LifecycleForgotten && !sha256Digest(rec.Digest) {
		return OmitMalformed
	}
	if rec.Binding.Principal == "" || rec.Binding.MemoryScope == "" {
		return OmitMalformed
	}
	return ""
}
