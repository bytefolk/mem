package search

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	memdb "github.com/PeterGuy326/mem/server/internal/db"
)

// Keep this regression when introducing a planner-compatible text route: a
// chunk candidate budget must not become a file result budget or bypass scope.
func TestTextANNFileSemanticsPostgres(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping text ANN PostgreSQL regression")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing non-test database %q", cfg.ConnConfig.Database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	owner, other := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{owner, other} {
		if _, err := database.Pool.Exec(ctx, "INSERT INTO users(id,email,password_hash) VALUES($1,$2,'test')", id, id.String()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := database.Pool.Exec(cleanupCtx, "DELETE FROM users WHERE id=ANY($1::uuid[])", []uuid.UUID{owner, other}); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}()
	now := time.Now().UTC().Truncate(time.Second)
	since, until := now.Add(-time.Hour), now.Add(time.Hour)
	vec := make([]float32, textEmbeddingSchemaDim)
	vec[0] = 1
	addFile := func(user uuid.UUID, path, mime string, at time.Time, chunks int, far bool) uuid.UUID {
		t.Helper()
		id := uuid.New()
		_, err := database.Pool.Exec(ctx, `INSERT INTO files(id,user_id,name,path,size,sha256,mime,storage_key,created_at,timeline_at)
			VALUES($1,$2,'fixture', $3,0,'fixture',$4,$1::text,$5,$5)`, id, user, path, mime, at)
		if err != nil {
			t.Fatal(err)
		}
		for chunk := 0; chunk < chunks; chunk++ {
			v := make([]float32, textEmbeddingSchemaDim)
			if far {
				v[1] = 1
			} else {
				v[0], v[1] = 1, float32(chunk)*0.001
			}
			_, err := database.Pool.Exec(ctx, `INSERT INTO embeddings_text(file_id,chunk_index,chunk_text,embedding)
				VALUES($1,$2,'source chunk',$3::vector)`, id, chunk, vectorLiteral(v))
			if err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	// 101 nearest chunks belong to one file. 40 candidates or even 8*k=80
	// candidates would yield only one file; the shipping route must return ten.
	best := addFile(owner, "/Work_%/Docs", "text/plain", now, 101, false)
	eligible := map[uuid.UUID]bool{best: true}
	for i := 0; i < 19; i++ {
		eligible[addFile(owner, "/Work_%/Docs", "application/pdf", now, 1, true)] = true
	}
	addFile(other, "/Work_%/Docs", "text/plain", now, 1, false)
	addFile(owner, "/Work_AB/Docs", "text/plain", now, 1, false)
	addFile(owner, "/Work_%/Private", "text/plain", now, 1, false)
	addFile(owner, "/Work_%/Docs", "image/png", now, 1, false)
	addFile(owner, "/Work_%/Docs", "text/plain", since.Add(-time.Second), 1, false)
	addFile(owner, "/Work_%/Docs", "text/plain", until.Add(time.Second), 1, false)
	service := New(database.Pool, nil)
	q := Query{UserID: owner, Limit: 10, PathPrefix: "/Work_%", AllowedPaths: []string{"/Work_%/Docs"}, Type: "doc", Since: &since, Until: &until, SnippetChars: 200}
	hits, err := service.runTextANN(ctx, q, vec)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 10 {
		t.Fatalf("got %d files, want 10 despite 101 nearest chunks belonging to one file", len(hits))
	}
	seen := map[uuid.UUID]bool{}
	for _, hit := range hits {
		if !eligible[hit.FileID] || seen[hit.FileID] {
			t.Fatalf("out-of-scope or duplicate file: %+v", hit)
		}
		seen[hit.FileID] = true
	}
	if hits[0].FileID != best || hits[0].ChunkIndex != 0 || hits[0].Score != 1 {
		t.Fatalf("best chunk was not preserved: %+v", hits[0])
	}
	q.AllowedPaths = []string{""}
	hits, err = service.runTextANN(ctx, q, vec)
	if err != nil || len(hits) != 0 {
		t.Fatalf("invalid allow-list must fail closed: hits=%v err=%v", hits, err)
	}
}
