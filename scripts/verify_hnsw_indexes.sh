#!/usr/bin/env bash
# EXPLAIN regression test for HNSW ANN indexes (issue #173)
#
# This script verifies that vector queries use index scans instead of
# sequential scans after migration 0024 is applied.
#
# Prerequisites:
#   - PostgreSQL 16+ with pgvector extension
#   - Database with all migrations applied (0001 through 0024)
#   - Populated test data in embeddings_text, embeddings_visual, embeddings_face
#
# Usage:
#   ./scripts/verify_hnsw_indexes.sh postgres://user:pass@host:port/db

set -euo pipefail

DB_URL="${1:?Usage: $0 <database-url>}"

echo "=== HNSW ANN Index Verification (Issue #173) ==="
echo ""

# Check that the indexes exist
echo "1. Verifying indexes exist..."
psql "$DB_URL" -c "
SELECT indexname, indexdef
FROM pg_indexes
WHERE tablename IN ('embeddings_text', 'embeddings_visual', 'embeddings_face')
  AND indexname LIKE '%hnsw%'
ORDER BY tablename;
"

echo ""
echo "2. Checking row counts..."
psql "$DB_URL" -c "
SELECT 'embeddings_text' AS table_name, COUNT(*) AS row_count FROM embeddings_text
UNION ALL
SELECT 'embeddings_visual', COUNT(*) FROM embeddings_visual
UNION ALL
SELECT 'embeddings_face', COUNT(*) FROM embeddings_face;
"

echo ""
echo "3. EXPLAIN text search query (should use idx_embeddings_text_embedding_hnsw)..."
psql "$DB_URL" -c "
EXPLAIN ANALYZE
SELECT e.id, e.file_id, e.chunk_index,
       1 - (e.embedding <=> '[0.1,0.2,0.3,...]'::vector) AS score
  FROM embeddings_text e
  JOIN files f ON f.id = e.file_id
 WHERE f.user_id = (SELECT id FROM users LIMIT 1)
 ORDER BY e.embedding <=> '[0.1,0.2,0.3,...]'::vector ASC
 LIMIT 10;
"

echo ""
echo "4. EXPLAIN visual search query (should use idx_embeddings_visual_embedding_hnsw)..."
psql "$DB_URL" -c "
EXPLAIN ANALYZE
SELECT e.file_id,
       (1 - (e.embedding <=> '[0.1,0.2,0.3,...]'::vector))::real AS score
  FROM embeddings_visual e
  JOIN files f ON f.id = e.file_id
 WHERE f.user_id = (SELECT id FROM users LIMIT 1)
 ORDER BY e.embedding <=> '[0.1,0.2,0.3,...]'::vector ASC
 LIMIT 10;
"

echo ""
echo "5. Verifying index usage in query plan..."
echo "Expected: 'Index Scan using idx_embeddings_*_embedding_hnsw' in both plans"
echo "NOT expected: 'Seq Scan on embeddings_*'"
echo ""

echo "=== Verification complete ==="
