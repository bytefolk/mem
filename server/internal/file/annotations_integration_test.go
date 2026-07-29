package file

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	memdb "github.com/PeterGuy326/mem/server/internal/db"
)

func TestAnnotationDecisionIntegration(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	userID, _ := createFileLockTenant(t, ctx, database.Pool, "file-annotation")
	fileID := uuid.New()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO files (
			id, user_id, name, path, size, sha256, mime, storage_key,
			tags, user_tags, summary, index_status
		)
		VALUES (
			$1, $2, 'photo.jpg', '/Photos', 1, $3, 'image/jpeg', $4,
			$5, $5, 'legacy summary', 'done'
		)
	`,
		fileID,
		userID,
		strings.Repeat("d", 64),
		"annotation-test/"+fileID.String(),
		[]string{"manual"},
	); err != nil {
		t.Fatalf("insert test file: %v", err)
	}

	tagID := uuid.New()
	descriptionID := uuid.New()
	rejectedManualTagID := uuid.New()
	concurrentTagID := uuid.New()
	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO file_annotations (
			id, file_id, stable_key, kind, value_text, confidence,
			source, provider, processor, analysis_version
		)
		VALUES
			($1, $4, 'tag:travel', 'tag', 'travel', 0.91,
			 'model', 'test-provider', 'image', 'v1'),
			($2, $4, 'description:primary', 'description', 'A summer trip', 0.87,
			 'model', 'test-provider', 'image', 'v1'),
			($3, $4, 'tag:manual', 'tag', 'manual', 0.51,
			 'model', 'test-provider', 'image', 'v1'),
			($5, $4, 'tag:concurrent', 'tag', 'concurrent', 0.73,
			 'model', 'test-provider', 'image', 'v1')
	`, tagID, descriptionID, rejectedManualTagID, fileID, concurrentTagID); err != nil {
		t.Fatalf("insert annotations: %v", err)
	}

	service := &Service{pool: database.Pool}
	tagResult, err := service.DecideAnnotation(
		ctx,
		userID,
		userID,
		fileID,
		tagID,
		AnnotationDecisionCommand{
			Decision:        AnnotationStatusAccepted,
			ExpectedVersion: 1,
			AllowedPaths:    []string{"/Photos"},
		},
	)
	if err != nil {
		t.Fatalf("accept tag: %v", err)
	}
	if tagResult.Replayed ||
		tagResult.Annotation.Status != AnnotationStatusAccepted ||
		tagResult.Annotation.StateVersion != 2 ||
		tagResult.Annotation.DecidedByUserID == nil ||
		*tagResult.Annotation.DecidedByUserID != userID ||
		tagResult.Annotation.DecidedAt == nil {
		t.Fatalf("accepted tag result = %+v", tagResult)
	}
	assertFileEnrichmentProjection(
		t,
		ctx,
		database.Pool,
		fileID,
		[]string{"manual", "travel"},
		"legacy summary",
	)

	replay, err := service.DecideAnnotation(
		ctx,
		userID,
		userID,
		fileID,
		tagID,
		AnnotationDecisionCommand{
			Decision:        AnnotationStatusAccepted,
			ExpectedVersion: 1,
			AllowedPaths:    []string{"/Photos"},
		},
	)
	if err != nil {
		t.Fatalf("replay tag acceptance: %v", err)
	}
	if !replay.Replayed || replay.Annotation.StateVersion != 2 {
		t.Fatalf("replay result = %+v", replay)
	}

	_, err = service.DecideAnnotation(
		ctx,
		userID,
		userID,
		fileID,
		tagID,
		AnnotationDecisionCommand{
			Decision:        AnnotationStatusRejected,
			ExpectedVersion: 2,
			AllowedPaths:    []string{"/Photos"},
		},
	)
	if !errors.Is(err, ErrAnnotationDecisionConflict) {
		t.Fatalf("opposite terminal decision error = %v", err)
	}

	_, err = service.DecideAnnotation(
		ctx,
		userID,
		userID,
		fileID,
		descriptionID,
		AnnotationDecisionCommand{
			Decision:        AnnotationStatusAccepted,
			ExpectedVersion: 1,
			AllowedPaths:    []string{"/Private"},
		},
	)
	if !errors.Is(err, ErrAnnotationNotFound) {
		t.Fatalf("path denial error = %v, want not found", err)
	}

	if _, err := service.DecideAnnotation(
		ctx,
		userID,
		userID,
		fileID,
		descriptionID,
		AnnotationDecisionCommand{
			Decision:        AnnotationStatusAccepted,
			ExpectedVersion: 1,
			AllowedPaths:    []string{"/Photos"},
		},
	); err != nil {
		t.Fatalf("accept description: %v", err)
	}
	assertFileEnrichmentProjection(
		t,
		ctx,
		database.Pool,
		fileID,
		[]string{"manual", "travel"},
		"A summer trip",
	)

	if _, err := service.DecideAnnotation(
		ctx,
		userID,
		userID,
		fileID,
		rejectedManualTagID,
		AnnotationDecisionCommand{
			Decision:        AnnotationStatusRejected,
			ExpectedVersion: 1,
			AllowedPaths:    []string{"/Photos"},
		},
	); err != nil {
		t.Fatalf("reject tag: %v", err)
	}
	assertFileEnrichmentProjection(
		t,
		ctx,
		database.Pool,
		fileID,
		[]string{"manual", "travel"},
		"A summer trip",
	)

	start := make(chan struct{})
	type concurrentDecision struct {
		result *AnnotationDecisionResult
		err    error
	}
	decisions := make(chan concurrentDecision, 2)
	for range 2 {
		go func() {
			<-start
			result, err := service.DecideAnnotation(
				ctx,
				userID,
				userID,
				fileID,
				concurrentTagID,
				AnnotationDecisionCommand{
					Decision:        AnnotationStatusAccepted,
					ExpectedVersion: 1,
					AllowedPaths:    []string{"/Photos"},
				},
			)
			decisions <- concurrentDecision{result: result, err: err}
		}()
	}
	close(start)

	var mutated, replayed int
	for range 2 {
		decision := <-decisions
		if decision.err != nil {
			t.Fatalf("concurrent decision: %v", decision.err)
		}
		if decision.result == nil ||
			decision.result.Annotation.Status != AnnotationStatusAccepted ||
			decision.result.Annotation.StateVersion != 2 {
			t.Fatalf("concurrent decision result = %+v", decision.result)
		}
		if decision.result.Replayed {
			replayed++
		} else {
			mutated++
		}
	}
	if mutated != 1 || replayed != 1 {
		t.Fatalf("concurrent outcomes mutated=%d replayed=%d, want 1/1", mutated, replayed)
	}
	assertFileEnrichmentProjection(
		t,
		ctx,
		database.Pool,
		fileID,
		[]string{"manual", "travel", "concurrent"},
		"A summer trip",
	)

	var (
		concurrentStatus  string
		concurrentVersion int64
		concurrentRows    int
	)
	if err := database.Pool.QueryRow(ctx, `
		SELECT min(status), min(state_version), count(*)
		  FROM file_annotations
		 WHERE file_id = $1
		   AND stable_key = 'tag:concurrent'
	`, fileID).Scan(&concurrentStatus, &concurrentVersion, &concurrentRows); err != nil {
		t.Fatalf("load concurrent annotation: %v", err)
	}
	if concurrentStatus != AnnotationStatusAccepted ||
		concurrentVersion != 2 ||
		concurrentRows != 1 {
		t.Fatalf(
			"concurrent annotation status=%q version=%d rows=%d",
			concurrentStatus,
			concurrentVersion,
			concurrentRows,
		)
	}

	detail, err := service.Get(ctx, userID, fileID)
	if err != nil {
		t.Fatalf("get enriched file: %v", err)
	}
	if len(detail.Annotations) != 4 {
		t.Fatalf("annotation count = %d, want 4", len(detail.Annotations))
	}
	if string(detail.SourceMetadata) != "{}" || string(detail.ProcessorMetadata) != "{}" {
		t.Fatalf(
			"metadata defaults = source:%s processor:%s",
			detail.SourceMetadata,
			detail.ProcessorMetadata,
		)
	}

	if _, err := database.Pool.Exec(ctx, `
		INSERT INTO file_annotations (
			file_id, stable_key, kind, value_text, confidence,
			source, provider, processor, analysis_version
		)
		SELECT
			$1,
			'bulk:' || value,
			'tag',
			'bulk-' || value,
			(value % 100)::real / 100,
			'model',
			'test-provider',
			'image',
			'bulk-v1'
		  FROM generate_series(1, $2) AS valueset(value)
	`, fileID, maxFileDetailAnnotations+10); err != nil {
		t.Fatalf("insert bounded annotation history: %v", err)
	}
	bounded, err := service.Get(ctx, userID, fileID)
	if err != nil {
		t.Fatalf("get bounded annotation history: %v", err)
	}
	if len(bounded.Annotations) != maxFileDetailAnnotations ||
		!bounded.AnnotationsTruncated {
		t.Fatalf(
			"bounded annotations=%d truncated=%t",
			len(bounded.Annotations),
			bounded.AnnotationsTruncated,
		)
	}
	if bounded.Annotations[0].ID != descriptionID ||
		bounded.Annotations[0].Kind != AnnotationKindDescription ||
		bounded.Annotations[0].Status != AnnotationStatusAccepted ||
		bounded.Summary == nil ||
		bounded.Annotations[0].ValueText != *bounded.Summary {
		t.Fatalf(
			"effective summary annotation missing from bounded detail: summary=%v first=%+v",
			bounded.Summary,
			bounded.Annotations[0],
		)
	}
	pending := make([]Annotation, 0, len(bounded.Annotations)-1)
	for _, annotation := range bounded.Annotations {
		if annotation.Status == AnnotationStatusPending {
			pending = append(pending, annotation)
		}
	}
	if len(pending) != maxFileDetailAnnotations-1 {
		t.Fatalf(
			"bounded pending annotations=%d, want %d",
			len(pending),
			maxFileDetailAnnotations-1,
		)
	}
	for index := 1; index < len(pending); index++ {
		if pending[index-1].Confidence < pending[index].Confidence {
			t.Fatalf(
				"pending confidence order at %d: %f < %f",
				index,
				pending[index-1].Confidence,
				pending[index].Confidence,
			)
		}
	}
}

func assertFileEnrichmentProjection(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fileID uuid.UUID,
	wantTags []string,
	wantSummary string,
) {
	t.Helper()
	var (
		tags    []string
		summary string
	)
	if err := pool.QueryRow(ctx, `
		SELECT tags, summary
		  FROM files
		 WHERE id = $1
	`, fileID).Scan(&tags, &summary); err != nil {
		t.Fatalf("load enrichment projection: %v", err)
	}
	if !reflect.DeepEqual(tags, wantTags) {
		t.Fatalf("tags = %#v, want %#v", tags, wantTags)
	}
	if summary != wantSummary {
		t.Fatalf("summary = %q, want %q", summary, wantSummary)
	}
}
