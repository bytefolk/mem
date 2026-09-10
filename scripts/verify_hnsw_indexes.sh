#!/usr/bin/env bash
# Read-only planner verification for shipping text and visual query shapes.
# Requires a populated disposable database; does not force planner settings.
set -euo pipefail
DB_URL="${1:?Usage: $0 <database-url> <corpus-user-uuid>}"
CORPUS_USER="${2:?Supply the user UUID that owns the populated corpus}"
[[ "$CORPUS_USER" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]] || {
  echo 'ERROR: corpus user must be a UUID' >&2; exit 1;
}
sql() { psql -X -A -t -v ON_ERROR_STOP=1 "$DB_URL" -c "$1"; }
db_name="$(sql 'SELECT current_database()')"
[[ "$db_name" == *_test ]] || { echo 'ERROR: database must end in _test' >&2; exit 1; }
pass=0
fail=0
for kind in text visual face; do
  index="idx_embeddings_${kind}_embedding_hnsw"
  valid="$(sql "SELECT count(*) FROM pg_index i
    JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_am a ON a.oid = c.relam
    WHERE i.indrelid = 'embeddings_${kind}'::regclass
      AND c.relname = '${index}' AND a.amname = 'hnsw' AND i.indisvalid
      AND pg_get_indexdef(i.indexrelid) LIKE '%vector_cosine_ops%'")"
  rows="$(sql "SELECT count(*) FROM embeddings_${kind} e JOIN files f ON f.id=e.file_id
    WHERE f.user_id='${CORPUS_USER}'::uuid AND e.embedding IS NOT NULL")"
  if [[ "$valid" == 1 && "$rows" -gt 0 ]]; then
    echo "PASS: ${index} is valid; corpus contains ${rows} non-null vectors"
    pass=$((pass + 1))
  else
    echo "FAIL: ${index}: valid=${valid}, corpus vectors=${rows}"
    fail=$((fail + 1))
  fi
done
assert_plan() {
  local route="$1" query="$2" plan
  # ANALYZE executes the read so vector/schema errors cannot hide behind EXPLAIN.
  plan="$(sql "EXPLAIN (ANALYZE, BUFFERS) ${query}")"
  echo "$plan"
  if grep -q "Index Scan using idx_embeddings_${route}_embedding_hnsw" <<<"$plan"; then
    echo "PASS: shipping ${route} query uses HNSW"
    pass=$((pass + 1))
  else
    echo "FAIL: shipping ${route} query does not use HNSW"
    fail=$((fail + 1))
  fi
}
# Match runTextANN: per-file DISTINCT ON precedes global top-k. A simple
# ORDER BY distance LIMIT probe would not establish this query's index usage.
assert_plan text "SELECT evidence_id, file_id, score FROM (
  SELECT DISTINCT ON (f.id) e.id::text AS evidence_id, f.id AS file_id,
    1 - (e.embedding <=> array_fill(0.1::real, ARRAY[768])::vector) AS score
  FROM embeddings_text e JOIN files f ON f.id=e.file_id
  WHERE f.user_id='${CORPUS_USER}'::uuid
  ORDER BY f.id, e.embedding <=> array_fill(0.1::real, ARRAY[768])::vector ASC
) hits ORDER BY score DESC LIMIT 10"
# Visual vectors have file_id (not e.id) and 512 dimensions.
assert_plan visual "SELECT e.file_id,
  (1 - (e.embedding <=> array_fill(0.1::real, ARRAY[512])::vector))::real AS score
  FROM embeddings_visual e JOIN files f ON f.id=e.file_id
  WHERE f.user_id='${CORPUS_USER}'::uuid
  ORDER BY e.embedding <=> array_fill(0.1::real, ARRAY[512])::vector ASC LIMIT 10"
echo "Results: ${pass} passed, ${fail} failed"
[[ "$fail" -eq 0 ]]
