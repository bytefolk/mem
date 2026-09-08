package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddingsTextUniqueConstraint(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping DB integration test")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse MEM_TEST_DB: %v", err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to modify non-test database %q", config.ConnConfig.Database)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	db := &DB{Pool: pool, url: dsn}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var userID, workspaceID, fileID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash) VALUES ('unique-ctest@test', 'x')
		ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspaces (name, resource_owner_user_id)
		VALUES ('unique-ctest-ws', $1)
		ON CONFLICT (resource_owner_user_id) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO files (user_id, name, path, size, sha256, mime, storage_key)
		VALUES ($1, 'unique-ctest.txt', '/unique-ctest.txt', 0, '', 'text/plain', 'test://unique')
		RETURNING id
	`, userID).Scan(&fileID); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM files WHERE id = $1`, fileID)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO embeddings_text (file_id, chunk_index, chunk_text, provider)
		VALUES ($1, 0, 'chunk zero', 'test')
	`, fileID); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO embeddings_text (file_id, chunk_index, chunk_text, provider)
		VALUES ($1, 0, 'duplicate chunk', 'test')
	`, fileID)
	if err == nil {
		t.Fatal("expected duplicate (file_id, chunk_index) to be rejected")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected unique violation 23505, got %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM embeddings_text WHERE file_id = $1`, fileID)
	})
}
