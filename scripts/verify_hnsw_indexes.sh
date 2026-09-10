#!/usr/bin/env bash
# EXPLAIN regression test for HNSW ANN indexes (issue #173)
#
# This script verifies that vector queries use index scans instead of
# sequential scans after migration 0025 is applied.
#
# Prerequisites:
#   - PostgreSQL 16+ with pgvector extension
#   - Database with all migrations applied (0001 through 0025)
#   - Populated test data in embeddings_text, embeddings_visual, embeddings_face
#
# Usage:
#   ./scripts/verify_hnsw_indexes.sh postgres://user:pass@host:port/db

set -euo pipefail

DB_URL="${1:?Usage: $0 <database-url>}"

pass=0
fail=0

assert_index_scan() {
  local label="$1"
  local table="$2"
  local plan
  plan="$(psql -AtX "$DB_URL" -c "
    EXPLAIN
    SELECT e.id
      FROM ${table} e
      JOIN files f ON f.id = e.file_id
     WHERE f.user_id = (SELECT id FROM users LIMIT 1)
     ORDER BY e.embedding <=> (SELECT array_fill(0.1, ARRAY[768]))::vector
     LIMIT 10;
  ")"
  if echo "$plan" | grep -qi "Index Scan.*hnsw"; then
    echo "PASS: ${label} uses HNSW index scan"
    pass=$((pass + 1))
  else
    echo "FAIL: ${label} does NOT use HNSW index scan"
    echo "$plan"
    fail=$((fail + 1))
  fi
}

echo "=== HNSW ANN Index Verification (Issue #173) ==="
echo ""

echo "1. Verifying indexes exist..."
index_count="$(psql -AtX "$DB_URL" -c "
SELECT COUNT(*)
FROM pg_indexes
WHERE tablename IN ('embeddings_text', 'embeddings_visual', 'embeddings_face')
  AND indexname LIKE '%hnsw%';
")"
if [[ "${index_count}" -ge 3 ]]; then
  echo "PASS: found ${index_count} HNSW indexes"
  pass=$((pass + 1))
else
  echo "FAIL: expected >= 3 HNSW indexes, found ${index_count}"
  fail=$((fail + 1))
fi

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
echo "3. Verifying index usage in query plans..."
assert_index_scan "text (768-d)" "embeddings_text"

echo ""
echo "=== Results: ${pass} passed, ${fail} failed ==="
[[ "${fail}" -eq 0 ]] || exit 1
