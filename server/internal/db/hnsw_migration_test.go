package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
)

// This is a schema/ingest gate, not the still-unmet shipping text planner gate.
// The runner owns a NEW database so populated upgrade/down/up cannot affect
// another suite's migration history or data.
func TestHNSWMigrationPostgres(t *testing.T) {
	dsn := os.Getenv("MEM_HNSW_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_HNSW_TEST_DB not set; requires a fresh owned test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing non-test database %q", cfg.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var history sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('goose_db_version')::text").Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history.Valid {
		t.Fatal("refusing existing migration history; provide a new owned test database")
	}
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, "migrations", 24); err != nil {
		t.Fatal(err)
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,password_hash)
		VALUES ('00000000-0000-0000-0000-000000000173','hnsw@example.test','test');
		INSERT INTO files(id,user_id,name,path,size,sha256,mime,storage_key)
		SELECT md5(i::text)::uuid,'00000000-0000-0000-0000-000000000173',
		  'fixture-' || i, '/hnsw', 0, 'fixture', 'text/plain', 'fixture-' || i
		FROM generate_series(1,2000) i`)
	for _, kind := range []string{"text", "visual", "face"} {
		dim, extraCols, extraValues := 512, "", ""
		if kind == "text" {
			dim, extraCols, extraValues = 768, ",chunk_index,chunk_text", ",0,'fixture'"
		}
		exec(fmt.Sprintf(`INSERT INTO embeddings_%s(file_id,embedding%s)
			SELECT id,array_fill(0.1::real,ARRAY[%d])::vector%s FROM files`, kind, extraCols, dim, extraValues))
	}
	assertState := func(wantIndexes, wantRows int) {
		t.Helper()
		for _, kind := range []string{"text", "visual", "face"} {
			var indexes, rows int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_index i
				JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am a ON a.oid=c.relam
				WHERE i.indrelid=($1::text)::regclass AND c.relname=$2 AND a.amname='hnsw'
				AND i.indisvalid AND pg_get_indexdef(i.indexrelid) LIKE '%vector_cosine_ops%'`,
				"embeddings_"+kind, "idx_embeddings_"+kind+"_embedding_hnsw").Scan(&indexes); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM embeddings_"+kind+" WHERE embedding IS NOT NULL").Scan(&rows); err != nil {
				t.Fatal(err)
			}
			if indexes != wantIndexes || rows != wantRows {
				t.Fatalf("%s: valid cosine indexes=%d want=%d, preserved vectors=%d want=%d", kind, indexes, wantIndexes, rows, wantRows)
			}
		}
	}
	assertState(0, 2000)
	if err := goose.UpToContext(ctx, db, "migrations", 25); err != nil {
		t.Fatal(err)
	}
	assertState(1, 2000)
	if err := goose.DownToContext(ctx, db, "migrations", 24); err != nil {
		t.Fatal(err)
	}
	assertState(0, 2000)
	if err := goose.UpToContext(ctx, db, "migrations", 25); err != nil {
		t.Fatal(err)
	}
	assertState(1, 2000)
	// Ingest remains writable after index construction (no concurrency claim).
	exec(`INSERT INTO files(id,user_id,name,path,size,sha256,mime,storage_key)
		VALUES (md5('2001')::uuid,'00000000-0000-0000-0000-000000000173',
		'after-index','/hnsw',0,'fixture','text/plain','after-index')`)
	for _, kind := range []string{"text", "visual", "face"} {
		dim, extraCols, extraValues := 512, "", ""
		if kind == "text" {
			dim, extraCols, extraValues = 768, ",chunk_index,chunk_text", ",0,'after-index'"
		}
		exec(fmt.Sprintf(`INSERT INTO embeddings_%s(file_id,embedding%s)
			VALUES (md5('2001')::uuid,array_fill(0.2::real,ARRAY[%d])::vector%s)`, kind, extraCols, dim, extraValues))
		_, err := db.ExecContext(ctx, fmt.Sprintf(`UPDATE embeddings_%s
			SET embedding=array_fill(0.1::real,ARRAY[%d])::vector WHERE file_id=md5('2001')::uuid`, kind, dim-1))
		if err == nil || !strings.Contains(err.Error(), "dimensions") {
			t.Fatalf("%s: wrong dimensionality must fail, got %v", kind, err)
		}
	}
	assertState(1, 2001)
	if err := (&DB{url: dsn}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS: populated 24->25->24->25, all three cosine indexes, 2000 vectors/table preserved, post-index ingest, dimension rejection and production startup")
}
