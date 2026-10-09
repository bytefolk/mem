package vaultsync_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/PeterGuy326/mem/server/internal/api"
	"github.com/PeterGuy326/mem/server/internal/auth"
	memdb "github.com/PeterGuy326/mem/server/internal/db"
	"github.com/PeterGuy326/mem/server/internal/vaultsync"
	"github.com/PeterGuy326/mem/server/internal/workspace"
)

// Uses only an explicitly selected isolated _test database. No production
// migration, credential recovery, or container restart occurs in this test.
func TestVaultSyncPostgres(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; real PostgreSQL Vault verification was not executed")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid isolated test database configuration")
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("refusing to modify a database whose name does not end in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal("could not connect to isolated test database")
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("isolated migrations: %v", err)
	}
	createTenant := func() (uuid.UUID, uuid.UUID) {
		user, ws := uuid.New(), uuid.New()
		if _, err := database.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'test-hash')`, user, user.String()+"@vault.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pool.Exec(ctx, `INSERT INTO workspaces(id,name,resource_owner_user_id) VALUES($1,'Vault test',$2)`, ws, user); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pool.Exec(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role) VALUES($1,$2,'owner')`, ws, user); err != nil {
			t.Fatal(err)
		}
		return user, ws
	}
	userA, wsA := createTenant()
	userB, wsB := createTenant()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = database.Pool.Exec(cleanupCtx, `DELETE FROM users WHERE id=$1 OR id=$2`, userA, userB)
	})
	service := vaultsync.New(database.Pool)
	vault, noteA, noteB := uuid.New(), uuid.New(), uuid.New()
	command := vaultsync.CommitCommand{WorkspaceID: wsA, ActorUserID: userA, VaultID: vault, Entries: []vaultsync.Mutation{
		{NoteID: noteA, Path: "岗位/第一份 空笔记.md", Content: ""}, {NoteID: noteB, Path: "共享/第二份 空笔记.md", Content: ""}}}
	empty, err := service.Snapshot(ctx, wsA, vault)
	if err != nil || empty.Revision != 0 || len(empty.Entries) != 0 {
		t.Fatal("new Vault snapshot", err)
	}
	first, err := service.Commit(ctx, command)
	if err != nil || first.Revision != 1 || len(first.Entries) != 2 {
		t.Fatalf("two independent empty notes: %+v %v", first, err)
	}
	// Two clients read the same head, then race a different edit. Exactly one
	// succeeds; the other gets a conflict without overwriting the winner.
	var wg sync.WaitGroup
	var lock sync.Mutex
	successes, conflicts := 0, 0
	for _, text := range []string{"客户端 A 内容", "客户端 B 内容"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			cmd := command
			cmd.BaseRevision = 1
			cmd.Entries = []vaultsync.Mutation{{NoteID: noteA, Path: command.Entries[0].Path, Content: text}}
			_, err := service.Commit(ctx, cmd)
			lock.Lock()
			defer lock.Unlock()
			var conflict *vaultsync.RevisionConflict
			if err == nil {
				successes++
			} else if errors.As(err, &conflict) && conflict.CurrentRevision == 2 {
				conflicts++
			} else {
				t.Errorf("unexpected concurrent result: %v", err)
			}
		}(text)
	}
	wg.Wait()
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS race successes=%d conflicts=%d", successes, conflicts)
	}
	second, err := service.Snapshot(ctx, wsA, vault)
	if err != nil || second.Revision != 2 || len(second.Entries) != 2 {
		t.Fatal("consistent snapshot after race", err)
	}
	other, err := service.Snapshot(ctx, wsB, vault)
	if err != nil || other.Revision != 0 || len(other.Entries) != 0 {
		t.Fatal("another workspace saw the Vault", err)
	}
	// Old text revisions remain unchanged and cannot be updated in place.
	var original string
	if err := database.Pool.QueryRow(ctx, `SELECT content FROM vault_entry_revisions WHERE workspace_id=$1 AND vault_id=$2 AND note_id=$3 AND revision=1`, wsA, vault, noteA).Scan(&original); err != nil || original != "" {
		t.Fatal("original revision changed", err)
	}
	if _, err := database.Pool.Exec(ctx, `UPDATE vault_entry_revisions SET content='rewrite' WHERE workspace_id=$1 AND vault_id=$2 AND note_id=$3 AND revision=1`, wsA, vault, noteA); err == nil {
		t.Fatal("immutable revision accepted UPDATE")
	}
	command.BaseRevision = 2
	command.Entries = []vaultsync.Mutation{{NoteID: noteA, Path: "岗位/第一份 空笔记.md", Deleted: true}}
	third, err := service.Commit(ctx, command)
	if err != nil || third.Revision != 3 || len(third.Entries) != 2 {
		t.Fatal("delete tombstone commit", err)
	}
	for _, entry := range third.Entries {
		if entry.NoteID == noteA && (!entry.Deleted || entry.Content != "") {
			t.Fatal("missing tombstone")
		}
	}
	// Test swap/rename under the actual unique index, then title-only commit.
	command.BaseRevision = 3
	command.Entries = []vaultsync.Mutation{{NoteID: noteA, Path: "共享/第二份 空笔记.md", Content: "恢复"}, {NoteID: noteB, Path: "岗位/第一份 空笔记.md", Content: "交换"}}
	fourth, err := service.Commit(ctx, command)
	if err != nil || fourth.Revision != 4 {
		t.Fatal("atomic path swap", err)
	}
	title := "组织知识库"
	command.BaseRevision = 4
	command.Entries = nil
	command.Title = &title
	fifth, err := service.Commit(ctx, command)
	if err != nil || fifth.Revision != 5 || fifth.Title != title {
		t.Fatal("title CAS", err)
	}
	listed, err := service.List(ctx, wsA)
	if err != nil || len(listed) != 1 || listed[0].VaultID != vault {
		t.Fatal("Vault discovery", err)
	}
	// Real HTTP + canonical auth + workspace middleware use sandbox-only tokens.
	authService := auth.New(database.Pool)
	ttl := time.Now().Add(time.Minute)
	writer, _, err := authService.CreateToken(ctx, userA, &wsA, "vault-test-writer", []string{auth.ScopeRead, auth.ScopeWrite}, nil, &ttl, false)
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := authService.CreateToken(ctx, userA, &wsA, "vault-test-reader", []string{auth.ScopeRead}, nil, &ttl, false)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := authService.CreateToken(ctx, userB, &wsB, "vault-test-foreign", []string{auth.ScopeRead}, nil, &ttl, false)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&api.Server{Auth: authService, Workspace: workspace.New(database.Pool), VaultSync: service, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Router())
	defer server.Close()
	request := func(method, route, token string, body any) (int, []byte) {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+route, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, payload
	}
	status, body := request("GET", "/v1/vault/snapshot?vaultId="+vault.String(), reader, nil)
	if status != 200 || !bytes.Contains(body, []byte("组织知识库")) {
		t.Fatal("authenticated snapshot HTTP", status)
	}
	status, _ = request("POST", "/v1/vault/commit", reader, map[string]any{"vaultId": vault, "baseRevision": 5, "title": "拒绝只读写入", "entries": []any{}})
	if status != 403 {
		t.Fatal("read-only credential could commit", status)
	}
	status, body = request("GET", "/v1/vault/snapshot?vaultId="+vault.String(), foreign, nil)
	if status != 200 || !bytes.Contains(body, []byte(`"revision":0`)) || bytes.Contains(body, []byte("组织知识库")) {
		t.Fatal("HTTP cross-workspace isolation", status)
	}
	status, body = request("POST", "/v1/vault/commit", writer, map[string]any{"vaultId": vault, "baseRevision": 1, "title": "stale", "entries": []any{}})
	if status != 409 || !bytes.Contains(body, []byte(`"currentRevision":5`)) {
		t.Fatalf("HTTP CAS conflict: %d %s", status, body)
	}
	// The additive migration is reversible on a disposable test deployment;
	// dropping Vault tables does not change existing files or auth tables.
	server.Close()
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal("could not open isolated migration rehearsal")
	}
	defer sqlDB.Close()
	goose.SetBaseFS(os.DirFS("../db"))
	if err := goose.DownToContext(ctx, sqlDB, "migrations", 26); err != nil {
		t.Fatalf("isolated Vault down migration: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("isolated Vault up migration: %v", err)
	}
}
