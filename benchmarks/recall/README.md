# Multilingual recall benchmark

> **Evidence boundary: producer checks do not complete the live benchmark.**
>
> The producer can emit `mem.recall-rankings.v1` from a running memd and copies
> engine/profile identity from that process. It still needs a populated real
> memd, actual embedding-provider output, and a saved ranking artifact before
> a real-memd baseline can land in `baselines/`. Unit or loopback HTTP fixtures
> establish transport behavior only. See
> [Exact remaining live prerequisites](#exact-remaining-live-prerequisites).

This directory provides a small, repeatable retrieval benchmark. It is a
decision aid for comparing lexical, vector and hybrid configurations; it is
not evidence of production recall.

The default run is deliberately model-free and offline:

```bash
python3 -m benchmarks.recall run --output /tmp/mem-recall.json
```

It prints a concise summary and writes canonical, sorted JSON. The artifact is
always labeled:

```text
engine=lexical-reference
not production recall
```

The lexical adapter reports `0 ms` latency as a deterministic sentinel. It
measures ranking behavior only and must not be used as a performance result.

## Dataset and privacy

[`data/v1/dataset.json`](data/v1/dataset.json) declares the dataset version,
coverage, provenance and license. The JSONL corpus and queries plus
`qrels.json` cover:

- structured memories, text files and image captions;
- Chinese and English;
- exact terms, paraphrases, metadata/path/workspace filters and hard
  negatives.

Every record is hand-authored synthetic data. Names, projects, paths,
preferences and captions are fictional; nothing was copied from a user,
private repository or internal dataset. The fixtures are released as
CC0-1.0.

## Metrics and deterministic rules

The runner reports overall metrics and groups them by scenario slice,
language and expected source kind:

- macro Recall@1, Recall@5 and Recall@10;
- macro reciprocal rank (MRR) and graded nDCG@10;
- citation and source-kind correctness;
- error and partial-result rates;
- nearest-rank p50/p95 latency;
- forbidden-source count and rate.

Rules are embedded in every artifact:

1. A result list is deduplicated by `doc_id`; the first occurrence and its
   citation win.
2. External order is authoritative. The lexical adapter sorts descending by
   score, then ascending by document ID for a stable tie.
3. Qrel grades use the bounded `0..3` scale. Recall divides by every qrel with
   relevance greater than zero; nDCG uses the numeric grades.
4. Every query is equally weighted in macro metrics. `partial` and `error`
   queries stay in the denominator; an error has an empty result list and
   therefore zero retrieval metrics.
5. Citation accuracy divides by every deduplicated returned result through
   rank 10. Empty results score zero.
6. Source correctness is one value per query: the first relevant result must
   have the expected source kind; no relevant result scores zero.
7. Latency percentiles use nearest rank across every query status.
8. A result is leakage when its document is unknown or violates any query
   workspace, source-kind, metadata or path-prefix filter. Path prefixes match
   complete path segments: `/notes` matches `/notes/item`, never
   `/notes-secret` or `/notes2`.

Leakage is a hard safety invariant, not a quality threshold. A run writes its
artifact for diagnosis and exits `2` when leakage is non-zero.

## Test and compare the checked baseline

Run the complete hermetic gate:

```bash
make test-recall
```

This executes unit tests, runs the lexical adapter twice to prove semantic
determinism apart from `generated_at`, compares it with
[`baselines/lexical-reference.v1.json`](baselines/lexical-reference.v1.json),
and proves the malicious cross-workspace fixture is detected with a non-zero
exit.

To produce explicit absolute values and deltas:

```bash
python3 -m benchmarks.recall run \
  --output /tmp/candidate.json \
  --compare benchmarks/recall/baselines/lexical-reference.v1.json \
  --comparison-output /tmp/comparison.json
```

Comparison JSON includes absolute values and deltas overall and for every
scenario, language and source-kind group. It is informational only. There is
intentionally no guessed quality threshold; a future gating policy requires
separate review and evidence from enough representative runs.

Maintainers can intentionally re-record the reference after reviewing corpus,
metric or lexical changes:

```bash
make recall-baseline
git diff -- benchmarks/recall/baselines/lexical-reference.v1.json
```

## Opt-in vector or hybrid rankings

The harness never calls a provider. Run the candidate system separately,
without putting text, credentials or vectors in the artifact, and export one
ranked row for every dataset query. Start from
[`fixtures/external-rankings.example.v1.json`](fixtures/external-rankings.example.v1.json):

```bash
python3 -m benchmarks.recall run \
  --rankings /path/to/rankings.json \
  --output /tmp/vector-candidate.json \
  --compare benchmarks/recall/baselines/lexical-reference.v1.json
```

The external document is provider-agnostic but strict:

```json
{
  "schema_version": "mem.recall-rankings.v1",
  "engine": "my-vector-run",
  "configuration": {
    "mode": "vector",
    "provider": "explicit-provider",
    "model": "explicit-model",
    "dimension": 768,
    "index": {"kind": "hnsw", "distance": "cosine"},
    "search": {"top_k": 10}
  },
  "hardware": {"host": "coarse non-identifying class"},
  "queries": [{
    "query_id": "q-...",
    "status": "ok",
    "latency_ms": 12.5,
    "results": [{
      "doc_id": "file-...",
      "citation": "mem://files/file-...",
      "score": 0.8
    }]
  }]
}
```

`mode` is explicitly `lexical`, `vector` or `hybrid`. Vector and hybrid runs
require provider, model and a positive vector dimension. A lexical run must
explicitly set those three fields to `null`. Every mode requires non-empty
index/search configuration and a coarse hardware summary; nothing is silently
inferred. Each dataset query must appear exactly once. If a provider produces
no ranking because it failed, include that query with `status: "error"`, a
finite non-negative `latency_ms`, an empty `results` array and an optional
bounded `error_code`. `partial` keeps its returned results. Omitting a query is
invalid because absence must not be mistaken for a successful empty result.

The artifact records repository commit/dirty state, dataset checksum,
configuration, Python/platform summary, coarse candidate hardware, generation
time and sanitized query failures. It does not copy query text, corpus text,
vectors or free-form provider errors. Credential-shaped configuration keys
such as `api_key`, `password`, `secret`, `token` and `authorization` are
rejected instead of being copied into an artifact.

## Live memd producer

The `produce` subcommand queries a running `memd` over file-search queries and
emits a `mem.recall-rankings.v1` file that the existing `run --rankings` path
consumes. Latency is measured client-side per request; the `0 ms` sentinel
warning above applies only to the offline lexical lane.

Engine, provider, model/profile identity and embedding dimension are read from
the running process (`GET /v1/version` and
`GET /v1/workspaces/current/ai-profile`). They are not CLI flags. The artifact
cannot be labeled `lexical-reference`. ANN identity is not advertised by those
endpoints, so `configuration.index.kind` is recorded as `not-advertised`
rather than guessed.

The default dataset is the multilingual v1 fixture. Scoring the same run
reports Recall@1/5/10 and nDCG@10 under `metrics.by_language` (`en`, `zh`)
and `metrics.by_source_kind` (`text`, `image_caption`, `structured`). The
Chinese set includes image-description queries (`q-zh-image-paraphrase`).
Structured-memory queries cannot be served by `POST /v1/search`; they are
recorded as `unsupported_source_kind` and do not fail the producer.

```bash
python3 -m benchmarks.recall produce \
  --memd-url http://localhost:8080 \
  --token "$MEM_TOKEN" \
  --output /tmp/live-rankings.json \
  --mode vector
```

Then score the saved rankings:

```bash
python3 -m benchmarks.recall run \
  --rankings /tmp/live-rankings.json \
  --output /tmp/live-artifact.json
```

Load the synthetic file corpus into an isolated test deployment first. The
producer does not ingest it. Supply a token bound to the dataset's workspace;
its labels do not establish the token's real workspace identity.

The producer maps each API result back to a dataset `doc_id` using the returned
folder `path` plus file `name`. Cross-workspace path collisions, unknown paths,
or ambiguous snippets fail the query instead of silently dropping evidence.
A same-workspace snippet-overlap tie also fails closed: document ID ordering
would invent identity evidence. Malformed paths, names, snippets and scores
fail with `invalid_result`, including malformed duplicate hits.
Query filters are translated where the API supports them: `path_prefix` becomes
`scope`, and `source_kind` becomes `type` (`image_caption` → `image`,
`text` → `text`). The `workspace` filter is not sent to the API because the
auth token determines workspace scope. A single token cannot select several
workspaces, so use a single-workspace fixture. Metadata filters are unsupported
and produce `unsupported_filter` without contacting the server; they are never
silently ignored.

Vector mode sends `route=text`; it does not claim a hybrid lexical/vector or
multimodal experiment. Lexical mode sends `route=lexical` and requires the
server capability from #183. It emits null provider/model/dimension as required
by the ranking schema. The `hardware.host` value deliberately records only the
producer client's OS/architecture, never its hostname. It is not the server's
hardware inventory and cannot establish comparable performance conditions.

The full v1 corpus also contains structured-memory queries. `/v1/search` cannot
serve these; the producer records `unsupported_source_kind` and exits 2. Any
HTTP, mapping or response error also exits 2 while retaining an error artifact.
Use the existing `profile-text-v1` file-only fixture for this producer's bounded
acceptance. An HTTP fixture test proves transport and artifact handling only;
it does not establish live provider quality, production latency, or full-corpus
acceptance. Those remain NOT VERIFIED until a real populated memd run is saved.


### Exact remaining live prerequisites

1. Use an isolated authorized memd/Worker deployment and disposable PostgreSQL
   database, and verify that its token is bound to the intended test workspace.
2. Load all five synthetic files from `data/profile-text-v1/corpus.jsonl`, keeping
   paths and content intact. Verify indexing and actual file identities before
   interpreting the producer's output. Direct database seeding can establish
   retrieval/transport behavior but does not verify ingestion or Worker indexing.
3. For vector acceptance, select the same actual embedding model for corpus and
   queries. The producer now copies provider/model/dimension from the active
   workspace AI profile; independently inspect the ANN index and deployment
   revision. `configuration.index.kind=not-advertised` means those endpoints
   do not expose ANN identity, not that no index exists.
4. Run the documented producer command, retain its output, score the resulting
   rankings and record errors and measured client latencies. An empty/error run
   or a fake embedding provider cannot establish vector quality. A lexical run
   requires the separate #194 server capability and remains a distinct result.
5. Score the v1 artifact's `metrics.by_language.zh` and
   `metrics.by_source_kind.image_caption` for GOAL.md §6. Structured-memory
   rows remain `unsupported_source_kind` because `/v1/search` is file search.

The producer remains **on-demand**. `make test-recall` and CI run deterministic
unit and fixture checks only; they never start memd or call a provider. No
real-memd baseline is checked into `benchmarks/recall/baselines/` until a
populated deployment's saved rankings are reviewed. That remaining #175
acceptance bullet is still open.
