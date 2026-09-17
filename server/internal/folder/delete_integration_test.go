package folder

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	memdb "github.com/PeterGuy326/mem/server/internal/db"
)

// trackingObjectStore records which keys are deleted so tests can assert that
// recursive folder delete cleans up blobs.
type trackingObjectStore struct {
	mu      sync.Mutex
	objects map[string]bool
	deleted []string
}

func newTrackingObjectStore() *trackingObjectStore {
	return &trackingObjectStore{objects: make(map[string]bool)}
}

func (s *trackingObjectStore) Put(_ context.Context, key string, _ io.Reader, _ int64, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = true
	return nil
}

func (s *trackingObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.objects[key] {
		return nil, &objectNotFoundError{key: key}
	}
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (s *trackingObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *trackingObjectStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objects[key]
}

func (s *trackingObjectStore) deleteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleted)
}

type objectNotFoundError struct{ key string }

func (e *objectNotFoundError) Error() string { return "object not found: " + e.key }

// TestRecursiveDeleteCleansUpBlobs verifies that recursive folder delete
// removes objects from the store after the DB rows are deleted.
//
//	MEM_TEST_DB=postgres://mem:mem@localhost:5432/mem_test?sslmode=disable \
//	  go test ./internal/folder -run TestRecursiveDeleteCleansUpBlobs
func TestRecursiveDeleteCleansUpBlobs(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping DB integration test")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse MEM_TEST_DB: %v", err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatalf(
			"refusing to modify non-test database %q; MEM_TEST_DB must end in _test",
			config.ConnConfig.Database,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	userID, _ := createFolderDeleteTenant(t, ctx, database.Pool)
	store := newTrackingObjectStore()
	service := New(database.Pool, WithStore(store))

	// Create a folder hierarchy with files.
	if _, err := service.Create(ctx, userID, "/Project/Sub"); err != nil {
		t.Fatalf("create folders: %v", err)
	}

	// Insert files with storage keys that the store tracks.
	fileKeys := []string{
		"users/" + userID.String() + "/" + uuid.NewString() + "/a.txt",
		"users/" + userID.String() + "/" + uuid.NewString() + "/b.txt",
		"users/" + userID.String() + "/" + uuid.NewString() + "/c.txt",
	}
	for i, key := range fileKeys {
		if err := store.Put(ctx, key, nil, 0, "text/plain"); err != nil {
			t.Fatalf("put object: %v", err)
		}
		paths := []string{"/Project", "/Project", "/Project/Sub"}
		names := []string{"a.txt", "b.txt", "c.txt"}
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO files (id, user_id, name, path, size, sha256, mime, storage_key, index_status)
			VALUES ($1, $2, $3, $4, 0, $5, 'text/plain', $6, 'ready')
		`, uuid.New(), userID, names[i], paths[i], strings.Repeat("x", 64), key); err != nil {
			t.Fatalf("insert file: %v", err)
		}
	}

	// Verify objects exist before delete.
	for _, key := range fileKeys {
		if !store.has(key) {
			t.Fatalf("object %s should exist before delete", key)
		}
	}

	// Recursive delete.
	if err := service.Delete(ctx, userID, "/Project", true); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}

	// Verify all objects were deleted from the store.
	for _, key := range fileKeys {
		if store.has(key) {
			t.Errorf("object %s should have been deleted from store", key)
		}
	}
	if store.deleteCount() != len(fileKeys) {
		t.Errorf("delete calls = %d, want %d", store.deleteCount(), len(fileKeys))
	}

	// Verify DB rows are gone.
	var fileCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM files WHERE user_id = $1
	`, userID).Scan(&fileCount); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if fileCount != 0 {
		t.Errorf("files remaining = %d, want 0", fileCount)
	}

	var folderCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM folders WHERE user_id = $1
	`, userID).Scan(&folderCount); err != nil {
		t.Fatalf("count folders: %v", err)
	}
	if folderCount != 0 {
		t.Errorf("folders remaining = %d, want 0", folderCount)
	}
}

// TestRecursiveDeleteWithoutStore verifies that recursive delete works without
// a store configured (backward compatibility — DB rows are deleted but blobs
// are not cleaned up).
func TestRecursiveDeleteWithoutStore(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping DB integration test")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse MEM_TEST_DB: %v", err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatalf(
			"refusing to modify non-test database %q; MEM_TEST_DB must end in _test",
			config.ConnConfig.Database,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	userID, _ := createFolderDeleteTenant(t, ctx, database.Pool)
	service := New(database.Pool) // no store

	if _, err := service.Create(ctx, userID, "/Orphan"); err != nil {
		t.Fatalf("create folder: %v", err)
	}
	key := "users/" + userID.String() + "/" + uuid.NewString() + "/orphan.txt"
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO files (id, user_id, name, path, size, sha256, mime, storage_key, index_status)
		VALUES ($1, $2, 'orphan.txt', '/Orphan', 0, $3, 'text/plain', $4, 'ready')
	`, uuid.New(), userID, strings.Repeat("y", 64), key); err != nil {
		t.Fatalf("insert file: %v", err)
	}

	// Delete should succeed even without a store.
	if err := service.Delete(ctx, userID, "/Orphan", true); err != nil {
		t.Fatalf("recursive delete without store: %v", err)
	}

	// DB row should be gone.
	var fileCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM files WHERE user_id = $1
	`, userID).Scan(&fileCount); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if fileCount != 0 {
		t.Errorf("files remaining = %d, want 0", fileCount)
	}
}

// TestRecursiveDeleteBlocksWhenMemoryCitesFileElsewhere is the #210 review
// regression: a memory living at /Work/task that cites a file under /Photos
// must block recursive delete of /Photos. Otherwise ON DELETE SET NULL plus
// blob cleanup would destroy the cited object while the memory stays active.
func TestRecursiveDeleteBlocksWhenMemoryCitesFileElsewhere(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping DB integration test")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse MEM_TEST_DB: %v", err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatalf(
			"refusing to modify non-test database %q; MEM_TEST_DB must end in _test",
			config.ConnConfig.Database,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	userID, workspaceID := createFolderDeleteTenant(t, ctx, database.Pool)

	store := newTrackingObjectStore()
	service := New(database.Pool, WithStore(store))
	if _, err := service.Create(ctx, userID, "/Photos"); err != nil {
		t.Fatalf("create /Photos: %v", err)
	}
	if _, err := service.Create(ctx, userID, "/Work"); err != nil {
		t.Fatalf("create /Work: %v", err)
	}
	photos, err := service.Get(ctx, userID, "/Photos")
	if err != nil {
		t.Fatalf("get /Photos: %v", err)
	}

	fileID := uuid.New()
	key := "users/" + userID.String() + "/" + fileID.String() + "/cited.txt"
	if err := store.Put(ctx, key, nil, 0, "text/plain"); err != nil {
		t.Fatalf("put object: %v", err)
	}
	sha := strings.Repeat("ab", 32)
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO files (id, user_id, folder_id, name, path, size, sha256, mime, storage_key, index_status)
		VALUES ($1, $2, $3, 'cited.txt', '/Photos', 0, $4, 'text/plain', $5, 'ready')
	`, fileID, userID, photos.ID, sha, key); err != nil {
		t.Fatalf("insert cited file: %v", err)
	}
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO memories (
			workspace_id, kind, content, path, source_type,
			source_file_id, source_file_sha256,
			idempotency_key_sha256, request_sha256, content_sha256,
			lifecycle_status
		) VALUES (
			$1, 'note', 'cites a photo', '/Work/task', 'agent',
			$2, $3,
			$3, $3, $3,
			'active'
		)
	`, workspaceID, fileID, sha); err != nil {
		t.Fatalf("insert citing memory: %v", err)
	}

	if err := service.Delete(ctx, userID, "/Photos", true); !errors.Is(err, ErrContainsMemories) {
		t.Fatalf("recursive delete with cross-path citation = %v, want ErrContainsMemories", err)
	}
	if !store.has(key) {
		t.Fatal("cited blob was deleted despite the blocking memory")
	}
	var fileCount int
	if err := database.Pool.QueryRow(ctx, `
		SELECT count(*) FROM files WHERE id = $1
	`, fileID).Scan(&fileCount); err != nil {
		t.Fatalf("count cited file: %v", err)
	}
	if fileCount != 1 {
		t.Fatalf("cited file remaining = %d, want 1", fileCount)
	}
	if _, err := service.Get(ctx, userID, "/Photos"); err != nil {
		t.Fatalf("/Photos changed despite blocked recursive delete: %v", err)
	}
}

func createFolderDeleteTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, uuid.UUID) {
	t.Helper()
	var userID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash)
		VALUES ($1, 'folder-delete-test')
		RETURNING id
	`, "folder-delete-"+uuid.NewString()+"@example.com").Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	})
	var workspaceID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspaces (name, resource_owner_user_id)
		VALUES ('folder-delete', $1)
		RETURNING id
	`, userID).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workspace_memberships (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("create workspace membership: %v", err)
	}
	return userID, workspaceID
}
