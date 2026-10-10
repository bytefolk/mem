package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/PeterGuy326/mem/server/internal/auth"
	"github.com/PeterGuy326/mem/server/internal/vaultsync"
)

type vaultStub struct {
	calls       int
	workspaceID uuid.UUID
	command     vaultsync.CommitCommand
	result      vaultsync.Snapshot
	vaults      []vaultsync.Summary
	err         error
}

func (s *vaultStub) Snapshot(_ context.Context, workspaceID, _ uuid.UUID) (vaultsync.Snapshot, error) {
	s.calls++
	s.workspaceID = workspaceID
	return s.result, s.err
}
func (s *vaultStub) List(_ context.Context, workspaceID uuid.UUID) ([]vaultsync.Summary, error) {
	s.calls++
	s.workspaceID = workspaceID
	return s.vaults, s.err
}
func (s *vaultStub) Commit(_ context.Context, cmd vaultsync.CommitCommand) (vaultsync.Snapshot, error) {
	s.calls++
	s.command = cmd
	return s.result, s.err
}

func TestVaultRoutesScopeWorkspaceAndConflict(t *testing.T) {
	id := uuid.New()
	body := `{"vaultId":"` + id.String() + `","baseRevision":0,"entries":[{"noteId":"` + uuid.NewString() + `","path":"中文/空笔记.md","content":"","properties":{"tools":["write"]}}]}`
	stub := &vaultStub{result: vaultsync.Snapshot{SchemaVersion: vaultsync.SchemaVersion, VaultID: id, Revision: 1, Entries: []vaultsync.Entry{}}}
	server := &Server{VaultSync: stub}
	req, actor, _, workspace := memoryHandlerContext(httptest.NewRequest(http.MethodPost, "/v1/vault/commit", strings.NewReader(body)), nil)
	req.Context().Value(ctxToken).(*auth.Token).Scopes = []string{auth.ScopeRead, auth.ScopeWrite}
	res := httptest.NewRecorder()
	server.requireScope(auth.ScopeRead)(server.requireScope(auth.ScopeWrite)(http.HandlerFunc(server.handleVaultCommit))).ServeHTTP(res, req)
	if res.Code != 200 || stub.command.WorkspaceID != workspace || stub.command.ActorUserID != actor {
		t.Fatalf("authenticated Vault binding: status=%d command=%+v", res.Code, stub.command)
	}
	stub.err = &vaultsync.RevisionConflict{CurrentRevision: 1}
	req, _, _, _ = memoryHandlerContext(httptest.NewRequest(http.MethodPost, "/v1/vault/commit", strings.NewReader(body)), nil)
	res = httptest.NewRecorder()
	server.handleVaultCommit(res, req)
	if res.Code != 409 || !strings.Contains(res.Body.String(), `"currentRevision":1`) {
		t.Fatalf("CAS conflict: %d %s", res.Code, res.Body.String())
	}
}

func TestVaultReadOnlyPathAndListIsolation(t *testing.T) {
	allowed, denied := uuid.New(), uuid.New()
	stub := &vaultStub{vaults: []vaultsync.Summary{{VaultID: allowed}, {VaultID: denied}}}
	server := &Server{VaultSync: stub}
	req, _, _, workspace := memoryHandlerContext(httptest.NewRequest(http.MethodGet, "/v1/vault/list", nil), []string{vaultsync.ScopePath(allowed)})
	res := httptest.NewRecorder()
	server.handleVaultList(res, req)
	if res.Code != 200 || stub.workspaceID != workspace || strings.Contains(res.Body.String(), denied.String()) || !strings.Contains(res.Body.String(), allowed.String()) {
		t.Fatal("Vault list leaked an unauthorized ID")
	}
	for _, scopes := range [][]string{{auth.ScopeRead}, {auth.ScopeWrite}} {
		req, _, _, _ = memoryHandlerContext(httptest.NewRequest(http.MethodPost, "/v1/vault/commit", strings.NewReader(`{}`)), nil)
		req.Context().Value(ctxToken).(*auth.Token).Scopes = scopes
		res = httptest.NewRecorder()
		server.requireScope(auth.ScopeRead)(server.requireScope(auth.ScopeWrite)(http.HandlerFunc(server.handleVaultCommit))).ServeHTTP(res, req)
		if res.Code != 403 {
			t.Fatal("commit did not require both read and write")
		}
	}
	previous := stub.calls
	req, _, _, _ = memoryHandlerContext(httptest.NewRequest(http.MethodGet, "/v1/vault/snapshot?vaultId="+denied.String(), nil), []string{vaultsync.ScopePath(allowed)})
	res = httptest.NewRecorder()
	server.handleVaultSnapshot(res, req)
	if res.Code != 403 || stub.calls != previous {
		t.Fatal("out-of-path snapshot reached storage")
	}
}

func TestVaultMalformedOversizedAndUnavailableFailClosed(t *testing.T) {
	stub := &vaultStub{}
	server := &Server{VaultSync: stub}
	for _, body := range []string{`{"vaultId":"bad","baseRevision":0,"entries":[]}`, `{"vaultId":"` + uuid.NewString() + `","entries":[]}`, `{"vaultId":"` + uuid.NewString() + `","baseRevision":0,"entries":[],"workspaceId":"override"}`, `{} {}`} {
		req, _, _, _ := memoryHandlerContext(httptest.NewRequest(http.MethodPost, "/v1/vault/commit", strings.NewReader(body)), nil)
		res := httptest.NewRecorder()
		server.handleVaultCommit(res, req)
		if res.Code != 400 || stub.calls != 0 {
			t.Fatalf("invalid body reached storage: %d", res.Code)
		}
	}
	big := `{"vaultId":"` + uuid.NewString() + `","baseRevision":0,"entries":[{"noteId":"` + uuid.NewString() + `","path":"note.md","content":"` + strings.Repeat("x", maxVaultCommitBodyBytes) + `"}]}`
	req, _, _, _ := memoryHandlerContext(httptest.NewRequest(http.MethodPost, "/v1/vault/commit", strings.NewReader(big)), nil)
	res := httptest.NewRecorder()
	server.handleVaultCommit(res, req)
	if res.Code != 413 || stub.calls != 0 {
		t.Fatal("oversize body reached storage")
	}
	stub.err = errors.New("private database credential")
	req, _, _, _ = memoryHandlerContext(httptest.NewRequest(http.MethodGet, "/v1/vault/snapshot?vaultId="+uuid.NewString(), nil), nil)
	res = httptest.NewRecorder()
	server.handleVaultSnapshot(res, req)
	if res.Code != 503 || strings.Contains(res.Body.String(), "credential") {
		t.Fatal("database failure leaked a diagnostic")
	}
}

func TestVaultRoutesRegistered(t *testing.T) {
	routes := (&Server{}).Router().(chi.Routes)
	seen := map[string]bool{}
	if err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"GET /v1/vault/list", "GET /v1/vault/snapshot", "POST /v1/vault/commit"} {
		if !seen[route] {
			t.Fatal("unregistered route", route)
		}
	}
	res := httptest.NewRecorder()
	(&Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Router().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/v1/vault/list", nil))
	if res.Code != 401 {
		t.Fatal("Vault route is public")
	}
	var body map[string]any
	if json.Unmarshal(res.Body.Bytes(), &body) != nil {
		t.Fatal("auth refusal must be JSON")
	}
}
