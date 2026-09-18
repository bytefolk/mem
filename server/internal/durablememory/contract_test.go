package durablememory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	exampleWorkspaceID = "11111111-1111-4111-8111-111111111111"
	exampleMemoryID    = "22222222-2222-4222-8222-222222222222"
	exampleGrantID     = "33333333-3333-4333-8333-333333333333"
	examplePrincipal   = "position.repo-owner"
	examplePosition    = "repo-owner"
	exampleScope       = "/workspaces/44444444-4444-4444-8444-444444444444/positions/repo-owner"
	exampleSourceDig   = "sha256:bee60ba20052b2969621c28f7378297c3b38cfa4eba66b22ff0df381447b2f8c"
	examplePermDig     = "sha256:98056de97087164dd9e0f5235cba6019d9576230faa1e37b104e735e5b5729a6"
	exampleTextDig     = "sha256:f695bb9d1be18d8498657537efa5effa2809dedd30dabcf5913d021c4fa9d9b7"
)

func TestDecodeRejectsFreeStringScope(t *testing.T) {
	raw := []byte(`{
		"contract": "durable-memory.v1",
		"memory_id": "` + exampleMemoryID + `",
		"kind": "project_decision",
		"scope": "/workspaces/ws_1/positions/repo-owner",
		"text": "Search APIs must match title OR body.",
		"trust": "untrusted",
		"authority": "none"
	}`)
	_, err := DecodeRecord(raw)
	if err == nil {
		t.Fatal("free-string scope must fail closed")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("want ErrMalformed, got %v", err)
	}
}

func TestPermissionDigestBindsWorkspacePrincipalAndScope(t *testing.T) {
	rec := validRecord(t)
	if rec.Grant.PermissionDigest != PermissionDigest(rec.Binding, rec.Grant.Mode, rec.Grant.GrantVersion) {
		t.Fatal("valid record permission_digest drifted from canonical tuple")
	}
	body := mustJSON(t, rec)
	var loose map[string]any
	if err := json.Unmarshal(body, &loose); err != nil {
		t.Fatal(err)
	}
	grant, _ := loose["grant"].(map[string]any)
	grant["permission_digest"] = exampleTextDig
	raw, err := json.Marshal(loose)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecord(raw); err == nil {
		t.Fatal("permission_digest that does not bind the grant tuple must fail")
	}
}

func TestDecodeRequiresPrincipalGrantBinding(t *testing.T) {
	rec := validRecord(t)
	body := mustJSON(t, rec)
	var loose map[string]any
	if err := json.Unmarshal(body, &loose); err != nil {
		t.Fatal(err)
	}
	delete(loose, "binding")
	raw, err := json.Marshal(loose)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecord(raw); err == nil {
		t.Fatal("record without binding must be rejected")
	}

	loose = map[string]any{}
	if err := json.Unmarshal(mustJSON(t, rec), &loose); err != nil {
		t.Fatal(err)
	}
	delete(loose, "grant")
	raw, err = json.Marshal(loose)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRecord(raw); err == nil {
		t.Fatal("record without grant must be rejected")
	}
}

func TestExampleRoundTripMatchesCheckedInSchemaShape(t *testing.T) {
	raw, err := os.ReadFile(examplePath(t))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := DecodeRecord(raw)
	if err != nil {
		t.Fatalf("example must decode: %v", err)
	}
	if rec.Contract != ContractVersion {
		t.Fatalf("contract = %q", rec.Contract)
	}
	if rec.Binding.Principal != examplePrincipal {
		t.Fatalf("principal = %q", rec.Binding.Principal)
	}
	if rec.Binding.MemoryScope != exampleScope {
		t.Fatalf("memory_scope = %q", rec.Binding.MemoryScope)
	}
	if rec.Grant.RevokedAt != nil {
		t.Fatal("example grant must be unrevoked")
	}
	if rec.Grant.PermissionDigest != examplePermDig {
		t.Fatalf("permission_digest = %q", rec.Grant.PermissionDigest)
	}
}

func TestRecallEligibility(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	caller := RecallCaller{
		WorkspaceID: exampleWorkspaceID,
		Principal:   examplePrincipal,
		MemoryScope: exampleScope,
		At:          now,
	}

	t.Run("active granted in-scope is eligible", func(t *testing.T) {
		rec := validRecord(t)
		got := EvaluateRecall(rec, caller)
		if !got.Eligible || got.OmitReason != "" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("expired unpinned is not eligible", func(t *testing.T) {
		rec := validRecord(t)
		expired := now.Add(-time.Hour)
		rec.ExpiresAt = &expired
		got := EvaluateRecall(rec, caller)
		if got.Eligible || got.OmitReason != OmitExpired {
			t.Fatalf("got %+v", got)
		}
		if PhysicalDeleteImplied(rec, caller) {
			t.Fatal("TTL must not imply physical delete of the source log")
		}
	})

	t.Run("pin preserves TTL eligibility but not permission", func(t *testing.T) {
		rec := validRecord(t)
		expired := now.Add(-time.Hour)
		rec.ExpiresAt = &expired
		rec.Pinned = true
		got := EvaluateRecall(rec, caller)
		if !got.Eligible {
			t.Fatalf("pinned expired record should remain eligible: %+v", got)
		}

		foreign := caller
		foreign.Principal = "position.other-agent"
		denied := EvaluateRecall(rec, foreign)
		if denied.Eligible || denied.OmitReason != OmitOutOfScope {
			t.Fatalf("pin must not enlarge permission: %+v", denied)
		}
	})

	t.Run("revoked grant is not eligible even when pinned", func(t *testing.T) {
		rec := validRecord(t)
		rec.Pinned = true
		revoked := now.Add(-time.Minute)
		rec.Grant.RevokedAt = &revoked
		got := EvaluateRecall(rec, caller)
		if got.Eligible || got.OmitReason != OmitRevoked {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("superseded archived forgotten malformed out-of-scope", func(t *testing.T) {
		cases := []struct {
			name   string
			mutate func(*Record)
			want   string
		}{
			{"superseded", func(r *Record) { r.Lifecycle = LifecycleSuperseded }, OmitSuperseded},
			{"archived", func(r *Record) { r.Lifecycle = LifecycleArchived }, OmitSuperseded},
			{"forgotten", func(r *Record) { r.Lifecycle = LifecycleForgotten; r.Text = "" }, OmitForgotten},
			{"malformed digest", func(r *Record) { r.Digest = "sha256:ab" }, OmitMalformed},
			{"cross principal", func(r *Record) { r.Binding.Principal = "position.other" }, OmitOutOfScope},
			{"cross workspace", func(r *Record) {
				r.Binding.WorkspaceID = uuid.MustParse("55555555-5555-4555-8555-555555555555")
			}, OmitOutOfScope},
			{"scope mismatch", func(r *Record) {
				r.Binding.MemoryScope = "/workspaces/44444444-4444-4444-8444-444444444444/positions/other"
			}, OmitOutOfScope},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				rec := validRecord(t)
				tc.mutate(&rec)
				got := EvaluateRecall(rec, caller)
				if got.Eligible || got.OmitReason != tc.want {
					t.Fatalf("got %+v want omit %s", got, tc.want)
				}
			})
		}
	})
}

func TestExactReadback(t *testing.T) {
	stored := validRecord(t)
	same := stored
	if err := ExactReadback(stored, same); err != nil {
		t.Fatalf("identical records must read back: %v", err)
	}

	tweaked := stored
	tweaked.Text = stored.Text + " (drift)"
	if err := ExactReadback(stored, tweaked); !errors.Is(err, ErrReadbackMismatch) {
		t.Fatalf("want ErrReadbackMismatch, got %v", err)
	}

	tweaked = stored
	tweaked.StateVersion++
	if err := ExactReadback(stored, tweaked); !errors.Is(err, ErrReadbackMismatch) {
		t.Fatalf("state_version drift must fail: %v", err)
	}
}

func TestForgetIsPermissionedAndNeverLocalFake(t *testing.T) {
	rec := validRecord(t)

	denied := EvaluateForget(rec, ForgetActor{
		WorkspaceID:               exampleWorkspaceID,
		Principal:                 examplePrincipal,
		MemoryScope:               exampleScope,
		TokenScopes:               []string{"read", "write"},
		WorkspaceRoleAllowsDelete: true,
	})
	if denied.Authorized || denied.LocalFake || denied.ErrorCode != ErrorForgetDenied {
		t.Fatalf("read/write grant must not forget: %+v", denied)
	}

	foreign := EvaluateForget(rec, ForgetActor{
		WorkspaceID:               exampleWorkspaceID,
		Principal:                 "position.other-agent",
		MemoryScope:               exampleScope,
		TokenScopes:               []string{"read", "write", "delete"},
		WorkspaceRoleAllowsDelete: true,
	})
	if foreign.Authorized || foreign.LocalFake || foreign.ErrorCode != ErrorForgetDenied {
		t.Fatalf("cross-principal forget must fail visibly: %+v", foreign)
	}

	ok := EvaluateForget(rec, ForgetActor{
		WorkspaceID:               exampleWorkspaceID,
		Principal:                 examplePrincipal,
		MemoryScope:               exampleScope,
		TokenScopes:               []string{"read", "write", "delete"},
		WorkspaceRoleAllowsDelete: true,
	})
	if !ok.Authorized || ok.LocalFake || ok.Deleted || ok.ErrorCode != "" {
		t.Fatalf("authorized forget is a server intent, not a local delete: %+v", ok)
	}
}

func TestReceiptSurfacesGrantAndRevocation(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	caller := RecallCaller{
		WorkspaceID: exampleWorkspaceID,
		Principal:   examplePrincipal,
		MemoryScope: exampleScope,
		At:          now,
	}

	rec := validRecord(t)
	receipt := BuildReceipt(rec, caller)
	if receipt.Contract != ContractVersion {
		t.Fatalf("contract = %q", receipt.Contract)
	}
	if !receipt.Eligible {
		t.Fatalf("eligible receipt: %+v", receipt)
	}
	if receipt.Grant.Status != GrantStatusActive {
		t.Fatalf("grant status = %q", receipt.Grant.Status)
	}
	if receipt.Grant.GrantID != rec.Grant.GrantID {
		t.Fatalf("grant id missing from receipt")
	}
	if receipt.Grant.PermissionDigest != rec.Grant.PermissionDigest {
		t.Fatal("permission digest must enter the receipt")
	}
	if receipt.Readback == nil || receipt.Readback.MemoryID != rec.MemoryID {
		t.Fatal("eligible receipt must carry exact readback")
	}

	revoked := now.Add(-time.Second)
	rec.Grant.RevokedAt = &revoked
	receipt = BuildReceipt(rec, caller)
	if receipt.Eligible || receipt.OmitReason != OmitRevoked {
		t.Fatalf("revoked receipt: %+v", receipt)
	}
	if receipt.Grant.Status != GrantStatusRevoked {
		t.Fatalf("revocation must be visible on receipt, got %q", receipt.Grant.Status)
	}
	if receipt.Readback != nil {
		t.Fatal("ineligible recall must not return payload readback")
	}
}

func TestCheckedInSchemaForbidsScopeProperty(t *testing.T) {
	raw, err := os.ReadFile(schemaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"scope"`) {
		t.Fatal("durable-memory.v1 schema must not declare a free scope string")
	}
	required := []string{`"binding"`, `"grant"`, `"principal"`, `"memory_scope"`, `"workspace_id"`, `"permission_digest"`, `"revoked_at"`}
	for _, key := range required {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("schema missing %s", key)
		}
	}
}

func validRecord(t *testing.T) Record {
	t.Helper()
	created := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	expires := time.Date(2026, 12, 17, 12, 0, 0, 0, time.UTC)
	conf := 0.7
	rec := Record{
		Contract: ContractVersion,
		MemoryID: uuid.MustParse(exampleMemoryID),
		Kind:     KindProjectDecision,
		Binding: Binding{
			WorkspaceID: uuid.MustParse(exampleWorkspaceID),
			PositionID:  examplePosition,
			Principal:   examplePrincipal,
			MemoryScope: exampleScope,
		},
		Grant: Grant{
			GrantID:      uuid.MustParse(exampleGrantID),
			Mode:         GrantModeRead,
			GrantVersion: 1,
			CapabilityGrant: CapabilityGrantRef{
				SchemaVersion: CapabilityGrantV1,
				Server:        "mem",
			},
			GrantedAt: created,
		},
		Source: Source{
			Kind:   SourceKindSegment,
			ID:     "seg_01",
			Digest: exampleSourceDig,
		},
		Citations:    []string{"turn:t33"},
		Producer:     Producer{AgentID: examplePrincipal, SessionID: "sess_1", TaskID: "task_1"},
		EventAt:      created.Add(-2 * time.Minute),
		CreatedAt:    created,
		ExpiresAt:    &expires,
		Importance:   "high",
		Confidence:   &conf,
		StateVersion: 1,
		Pinned:       false,
		Lifecycle:    LifecycleActive,
		Trust:        TrustUntrusted,
		Authority:    AuthorityNone,
		Text:         "Search APIs must match title OR body and return matchField.",
		Digest:       exampleTextDig,
	}
	rec.Grant.PermissionDigest = PermissionDigest(rec.Binding, rec.Grant.Mode, rec.Grant.GrantVersion)
	return rec
}

func mustJSON(t *testing.T, rec Record) []byte {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "schemas")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func examplePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "docs", "examples", "durable-memory.v1.example.json")
}

func schemaPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "docs", "schemas", "durable-memory.v1.schema.json")
}
