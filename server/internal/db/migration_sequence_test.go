package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

func contiguousMigrationHead(t *testing.T) int {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range entries {
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil || version != i+1 {
			t.Fatalf("migration sequence gap: want %04d, got %q; include predecessors before deployment", i+1, entry.Name())
		}
	}
	return len(entries)
}

func TestMigrationFilesContiguous(t *testing.T) {
	contiguousMigrationHead(t)
}

// The runner supplies a NEW, owned database, distinct from the shared
// MEM_TEST_DB integration fixture. Never roll back or renumber deployed DDL.
func TestMigrationUpgradeSequence(t *testing.T) {
	head := contiguousMigrationHead(t)
	dsn := os.Getenv("MEM_MIGRATION_SEQUENCE_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_MIGRATION_SEQUENCE_TEST_DB not set; requires a fresh owned test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing non-test database %q", cfg.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	var existing sql.NullString
	if err := sqldb.QueryRowContext(ctx, "SELECT to_regclass('goose_db_version')::text").Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing.Valid {
		t.Fatal("sequence regression requires a fresh database; refusing an existing migration history")
	}
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	var userID, fileID uuid.UUID
	for version := 23; version <= head; version++ {
		// Match production's strict Goose behavior: no AllowMissing option.
		if err := goose.UpToContext(ctx, sqldb, "migrations", int64(version)); err != nil {
			t.Fatalf("upgrade to %d: %v", version, err)
		}
		actual, err := goose.GetDBVersionContext(ctx, sqldb)
		if err != nil || actual != int64(version) {
			t.Fatalf("migration head = %d, want %d, err=%v", actual, version, err)
		}
		var applied int
		if err := sqldb.QueryRowContext(ctx, "SELECT count(DISTINCT version_id) FROM goose_db_version WHERE is_applied AND version_id BETWEEN 1 AND $1", version).Scan(&applied); err != nil || applied != version {
			t.Fatalf("applied history has %d of %d predecessors, err=%v", applied, version, err)
		}
		if version == 23 {
			if err := sqldb.QueryRowContext(ctx, "INSERT INTO users(email,password_hash) VALUES($1,'test') RETURNING id", uuid.NewString()+"@example.test").Scan(&userID); err != nil {
				t.Fatal(err)
			}
			if err := sqldb.QueryRowContext(ctx, "INSERT INTO files(user_id,name,path,size,sha256,mime,storage_key) VALUES($1,'migration-sequence.txt','/fixture',0,'fixture','text/plain','test://sequence') RETURNING id", userID).Scan(&fileID); err != nil {
				t.Fatal(err)
			}
			if _, err := sqldb.ExecContext(ctx, "INSERT INTO embeddings_text(file_id,chunk_index,chunk_text,embedding) SELECT $1,0,'populated duplicate',array_fill(0.1::real,ARRAY[768])::vector FROM generate_series(1,2)", fileID); err != nil {
				t.Fatal(err)
			}
		}
		if version >= 24 {
			var lexical bool
			if err := sqldb.QueryRowContext(ctx, "SELECT search_tsv @@ plainto_tsquery('simple','migration-sequence.txt') FROM files WHERE id=$1", fileID).Scan(&lexical); err != nil || !lexical {
				t.Fatalf("populated lexical backfill: %v, err=%v", lexical, err)
			}
		}
		if version >= 25 {
			var indexes int
			if err := sqldb.QueryRowContext(ctx, `SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am am ON am.oid=c.relam
				WHERE i.indisvalid AND am.amname='hnsw' AND c.relname IN
				('idx_embeddings_text_embedding_hnsw','idx_embeddings_visual_embedding_hnsw','idx_embeddings_face_embedding_hnsw')`).Scan(&indexes); err != nil || indexes != 3 {
				t.Fatalf("valid HNSW indexes=%d, err=%v", indexes, err)
			}
		}
		var chunks int
		wantChunks := 2
		if version >= 26 {
			wantChunks = 1
		}
		if err := sqldb.QueryRowContext(ctx, "SELECT count(*) FROM embeddings_text WHERE file_id=$1", fileID).Scan(&chunks); err != nil || chunks != wantChunks {
			t.Fatalf("preserved chunks=%d, want=%d, err=%v", chunks, wantChunks, err)
		}
		t.Logf("PASS: strict Goose upgrade to %d; complete history and populated data preserved", version)
	}
	if head >= 26 {
		_, err := sqldb.ExecContext(ctx, "INSERT INTO embeddings_text(file_id,chunk_index,chunk_text) VALUES($1,0,'duplicate')", fileID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("expected duplicate rejection 23505, got %v", err)
		}
	}
	// The real startup path must accept the upgraded history unchanged.
	if err := (&DB{url: dsn}).Migrate(ctx); err != nil {
		t.Fatalf("production startup after sequential upgrade: %v", err)
	}
}
