package folder

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/PeterGuy326/mem/server/internal/storage"
)

// TestRecursiveDeleteCleansBlobs verifies that a recursive folder delete
// removes objects from bucket storage, not just DB rows. It requires a live
// PostgreSQL instance (MEM_TEST_DB) and a MinIO endpoint (MEM_TEST_S3_ENDPOINT).
func TestRecursiveDeleteCleansBlobs(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping")
	}
	s3Endpoint := os.Getenv("MEM_TEST_S3_ENDPOINT")
	if s3Endpoint == "" {
		t.Skip("MEM_TEST_S3_ENDPOINT not set; skipping")
	}

	s3AccessKey := os.Getenv("MEM_TEST_S3_ACCESS_KEY")
	if s3AccessKey == "" {
		s3AccessKey = "mem"
	}
	s3SecretKey := os.Getenv("MEM_TEST_S3_SECRET_KEY")
	if s3SecretKey == "" {
		s3SecretKey = "mem-minio-password"
	}
	s3Bucket := os.Getenv("MEM_TEST_S3_BUCKET")
	if s3Bucket == "" {
		s3Bucket = "mem"
	}

	ctx := context.Background()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse MEM_TEST_DB: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing non-test database %q", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	store, err := storage.New(ctx, s3Endpoint, s3AccessKey, s3SecretKey, s3Bucket, "", false)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}

	userID := folderTestUUID(t, ctx, pool,
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id`,
		"folder-blob-cleanup-"+uuid.NewString()+"@example.com")
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	}()
	folderTestUUID(t, ctx, pool,
		`INSERT INTO workspaces (name, resource_owner_user_id)
		 VALUES ('folder blob cleanup test', $1) RETURNING id`, userID)

	svc := New(pool, store, nil)

	if _, err := svc.Create(ctx, userID, "/CleanupParent/Child"); err != nil {
		t.Fatalf("create folders: %v", err)
	}

	childFolder, err := svc.Get(ctx, userID, "/CleanupParent/Child")
	if err != nil {
		t.Fatalf("get child folder: %v", err)
	}

	type testFile struct {
		id         uuid.UUID
		name       string
		path       string
		folderID   uuid.UUID
		storageKey string
	}
	files := []testFile{
		{
			id:         uuid.New(),
			name:       "a.txt",
			path:       "/CleanupParent",
			folderID:   mustFolderID(t, ctx, pool, userID, "/CleanupParent"),
			storageKey: fmt.Sprintf("users/%s/%s/a.txt", userID, uuid.New()),
		},
		{
			id:         uuid.New(),
			name:       "b.txt",
			path:       "/CleanupParent/Child",
			folderID:   childFolder.ID,
			storageKey: fmt.Sprintf("users/%s/%s/b.txt", userID, uuid.New()),
		},
	}

	for _, f := range files {
		if err := store.Put(ctx, f.storageKey, bytes.NewReader([]byte("content-"+f.name)), int64(len("content-"+f.name)), "text/plain"); err != nil {
			t.Fatalf("put object %s: %v", f.storageKey, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO files (id, user_id, name, path, folder_id, size, sha256, mime, storage_key, tags, index_status)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, 'text/plain', $8, '{}', 'pending')`,
			f.id, userID, f.name, f.path, f.folderID, len("content-"+f.name),
			"sha256-"+f.id.String(), f.storageKey,
		); err != nil {
			t.Fatalf("insert file row %s: %v", f.name, err)
		}
	}

	for _, f := range files {
		reader, err := store.Get(ctx, f.storageKey)
		if err != nil {
			t.Fatalf("precondition: object %s should exist before delete: %v", f.storageKey, err)
		}
		content, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || string(content) != "content-"+f.name {
			t.Fatalf("precondition: object %s content=%q read=%v close=%v", f.storageKey, content, readErr, closeErr)
		}
	}

	if err := svc.Delete(ctx, userID, "/CleanupParent", true); err != nil {
		t.Fatalf("recursive delete: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM files WHERE user_id = $1 AND path LIKE '/CleanupParent%'`,
		userID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining files: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected 0 file rows after recursive delete, got %d", remaining)
	}

	for _, f := range files {
		reader, err := store.Get(ctx, f.storageKey)
		if err == nil {
			_ = reader.Close()
			t.Errorf("object %s still exists in bucket after recursive delete", f.storageKey)
			continue
		}
		var response minio.ErrorResponse
		if !errors.As(err, &response) || response.Code != "NoSuchKey" {
			t.Errorf("object %s: expected S3 NoSuchKey after recursive delete, got %v", f.storageKey, err)
		}
	}
}

func mustFolderID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, path string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT id FROM folders WHERE user_id = $1 AND path = $2`, userID, path).Scan(&id); err != nil {
		t.Fatalf("lookup folder %q: %v", path, err)
	}
	return id
}
