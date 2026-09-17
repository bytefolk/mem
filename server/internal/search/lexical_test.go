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
		{"meeting_notes.md", "/Work/NotesExtra", "text/markdown"},
		{"literal_scope.txt", "/Work/100%_done", "text/plain"},
		{"literal_scope.txt", "/Work/100XXdone", "text/plain"},
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

	t.Run("substring match for CJK filename", func(t *testing.T) {
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
		if len(hits) != 1 || hits[0].Name != "meeting_notes.md" || hits[0].Path != "/Work/Notes" {
			t.Fatalf("path filter should return only the matching subtree: %+v", hits)
		}
	})
	t.Run("trigram typo retains weak matches without similarity prefilter", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{UserID: userID, Route: RouteLexical, Text: "quaterly", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range hits {
			if hit.Name == "quarterly_report.pdf" {
				if hit.Score < 0.20 || hit.Score >= 0.70 {
					t.Fatalf("typo should be ranked in the trigram tier: %+v", hit)
				}
				return
			}
		}
		t.Fatalf("trigram-only typo was dropped: %+v", hits)
	})

	t.Run("literal authorized subtree", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{
			UserID: userID, Route: RouteLexical, Text: "literal_scope", Limit: 10,
			AllowedPaths: []string{"/Work/100%_done"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].Path != "/Work/100%_done" {
			t.Fatalf("literal authorization scope leaked or lost results: %+v", hits)
		}
	})

	t.Run("MIME and time filters", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{UserID: userID, Route: RouteLexical, Text: "beach", Type: "image"})
		if err != nil || len(hits) != 1 || hits[0].Name != "photo_beach.jpg" {
			t.Fatalf("image filter: hits=%+v err=%v", hits, err)
		}
		after := now.Add(time.Second)
		before := now.Add(-time.Second)
		for _, q := range []Query{
			{Type: "audio"}, {Since: &after}, {Until: &before},
			{AllowedPaths: []string{"/Private"}},
			{AllowedPaths: []string{""}},
		} {
			q.UserID, q.Route, q.Text = userID, RouteLexical, "beach"
			hits, err := service.Search(ctx, q)
			if err != nil || len(hits) != 0 {
				t.Fatalf("filter %+v: hits=%+v err=%v", q, hits, err)
			}
		}
	})

	t.Run("other owner cannot retrieve files", func(t *testing.T) {
		hits, err := service.Search(ctx, Query{UserID: uuid.New(), Route: RouteLexical, Text: "quarterly_report"})
		if err != nil || len(hits) != 0 {
			t.Fatalf("other-owner search: hits=%+v err=%v", hits, err)
		}
	})

}

// Query validation belongs to the exported service entry point and must happen
// before any database or worker access, including the model-free route.
func TestLexicalSearchRejectsEmptyQuery(t *testing.T) {
	for _, text := range []string{"", " ", "\n\t"} {
		_, err := New(nil, nil).Search(context.Background(), Query{
			UserID: uuid.New(), Route: RouteLexical, Text: text,
		})
		if err == nil || !strings.Contains(err.Error(), "query is empty") {
			t.Fatalf("empty query %q returned %v", text, err)
		}
	}
}
