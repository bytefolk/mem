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

func TestLexicalSearchWithoutWorker(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping lexical search PostgreSQL test")
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
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash)
		VALUES ($1, $2, 'test-only')
	`, userID, "lexical-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		database.Pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, userID)
	})

	now := time.Now().UTC().Truncate(time.Second)
	files := []struct {
		name string
		path string
		mime string
	}{
		{"quarterly_report.pdf", "/Work/Reports", "application/pdf"},
		{"meeting_notes.md", "/Work/Notes", "text/markdown"},
		{"photo_beach.jpg", "/Photos", "image/jpeg"},
		{"年度总结.docx", "/Work", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
	}
	for _, f := range files {
		if _, err := database.Pool.Exec(ctx, `
			INSERT INTO files (
			    user_id, name, path, size, sha256, mime, storage_key,
			    index_status, created_at, updated_at
			) VALUES (
			    $1, $2, $3, 1, $4, $5, $6,
			    'ready', $7, $7
			)
		`, userID, f.name, f.path, strings.Repeat("b", 64), f.mime,
			"test/"+uuid.NewString(), now); err != nil {
			t.Fatal(err)
		}
	}

	// Service with nil worker — the key precondition for this test.
	service := New(database.Pool, nil)

	t.Run("lexical route works without worker", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{
			UserID: userID,
			Text:   "report",
			Route:  RouteLexical,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("lexical search failed: %v", err)
		}
		if len(hits) == 0 {
			t.Fatal("lexical search returned no results for 'report'")
		}
		found := false
		for _, h := range hits {
			if h.Name == "quarterly_report.pdf" {
				found = true
				if h.Source != RouteLexical {
					t.Errorf("hit source = %q, want %q", h.Source, RouteLexical)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected quarterly_report.pdf in results, got %+v", hits)
		}
	})

	t.Run("text route fails without worker", func(t *testing.T) {
		_, err := service.Search(ctx, Query{
			UserID: userID,
			Text:   "report",
			Route:  RouteText,
			Limit:  10,
		})
		if err == nil {
			t.Fatal("text route should fail without worker")
		}
		if !strings.Contains(err.Error(), "worker not configured") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("auto route fails without worker", func(t *testing.T) {
		_, err := service.Search(ctx, Query{
			UserID: userID,
			Text:   "report",
			Route:  RouteAuto,
			Limit:  10,
		})
		if err == nil {
			t.Fatal("auto route should fail without worker")
		}
		if !strings.Contains(err.Error(), "worker not configured") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("trigram match for CJK filename", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{
			UserID: userID,
			Text:   "总结",
			Route:  RouteLexical,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("lexical CJK search failed: %v", err)
		}
		if len(hits) == 0 {
			t.Fatal("lexical search returned no results for CJK query '总结'")
		}
	})

	t.Run("path filter applies to lexical", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{
			UserID:     userID,
			Text:       "notes",
			Route:      RouteLexical,
			PathPrefix: "/Work/Notes",
			Limit:      10,
		})
		if err != nil {
			t.Fatalf("lexical path-filtered search failed: %v", err)
		}
		for _, h := range hits {
			if h.Name == "meeting_notes.md" && h.Path != "/Work/Notes" {
				t.Errorf("path filter leaked: got path %q", h.Path)
			}
		}
	})
}
