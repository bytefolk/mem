"""Produce mem.recall-rankings.v1 from a live memd instance.

Queries each dataset query against POST /v1/search, maps API results back to
dataset doc_ids by path, and emits the rankings JSON that the existing harness
consumes via --rankings.
"""

from __future__ import annotations

import argparse
import json
import platform
import socket
import time
import unicodedata
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from .dataset import Dataset, Document, load_dataset

PACKAGE_ROOT = Path(__file__).resolve().parent
DEFAULT_DATASET = PACKAGE_ROOT / "data" / "v1"

_SOURCE_KIND_TO_TYPE = {
    "image_caption": "image",
    "text": "text",
}


def _build_path_index(documents: list[Document]) -> dict[str, list[Document]]:
    index: dict[str, list[Document]] = {}
    for doc in documents:
        index.setdefault(doc.path, []).append(doc)
    return index


def _match_doc_by_path(
    api_path: str,
    snippet: str,
    candidates: list[Document],
) -> Document | None:
    if not candidates:
        return None
    if len(candidates) == 1:
        return candidates[0]
    normalized_snippet = unicodedata.normalize("NFKC", snippet).casefold()
    best: Document | None = None
    best_overlap = -1
    for doc in candidates:
        doc_tokens = set(unicodedata.normalize("NFKC", doc.text).casefold().split())
        overlap = sum(1 for t in normalized_snippet.split() if t in doc_tokens)
        if overlap > best_overlap:
            best_overlap = overlap
            best = doc
    return best


def _source_kind_to_api_type(source_kind: str) -> str | None:
    return _SOURCE_KIND_TO_TYPE.get(source_kind)


def _coarse_host() -> str:
    try:
        return f"{platform.system()}/{platform.machine()}"
    except Exception:
        return "unknown"


def _query_memd(
    base_url: str,
    token: str,
    query_text: str,
    *,
    scope: str = "",
    type_filter: str = "",
    limit: int = 10,
    timeout: float = 30.0,
) -> tuple[list[dict[str, Any]], float, str | None]:
    body: dict[str, Any] = {"query": query_text, "limit": limit}
    if scope:
        body["scope"] = scope
    if type_filter:
        body["type"] = type_filter

    url = base_url.rstrip("/") + "/v1/search"
    data = json.dumps(body).encode("utf-8")
    req = Request(url, data=data, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", f"Bearer {token}")

    start = time.perf_counter()
    try:
        with urlopen(req, timeout=timeout) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
        elapsed_ms = (time.perf_counter() - start) * 1000.0
        results = payload.get("results", [])
        return results, elapsed_ms, None
    except HTTPError as exc:
        elapsed_ms = (time.perf_counter() - start) * 1000.0
        return [], elapsed_ms, f"http_{exc.code}"
    except URLError:
        elapsed_ms = (time.perf_counter() - start) * 1000.0
        return [], elapsed_ms, "connection_error"
    except Exception:
        elapsed_ms = (time.perf_counter() - start) * 1000.0
        return [], elapsed_ms, "unknown_error"


def produce_rankings(
    dataset: Dataset,
    *,
    base_url: str,
    token: str,
    limit: int = 10,
    timeout: float = 30.0,
    engine_label: str = "live-memd",
    dimension: int = 1536,
    mode: str = "hybrid",
    provider: str = "memd",
    model: str = "memd-embedded",
) -> dict[str, Any]:
    path_index = _build_path_index(list(dataset.documents))

    query_rows: list[dict[str, Any]] = []
    for query in dataset.queries:
        scope = query.filters.get("path_prefix", "")
        type_filter = _source_kind_to_api_type(query.expected_source_kind) or ""

        api_results, latency_ms, error_code = _query_memd(
            base_url,
            token,
            query.text,
            scope=scope,
            type_filter=type_filter,
            limit=limit,
            timeout=timeout,
        )

        if error_code and not api_results:
            row: dict[str, Any] = {
                "query_id": query.id,
                "status": "error",
                "latency_ms": round(latency_ms, 2),
                "results": [],
                "error_code": error_code,
            }
            query_rows.append(row)
            continue

        mapped_results: list[dict[str, Any]] = []
        seen_doc_ids: set[str] = set()
        for hit in api_results:
            hit_path = hit.get("path", "")
            snippet = hit.get("snippet", "")
            candidates = path_index.get(hit_path, [])
            doc = _match_doc_by_path(hit_path, snippet, candidates)
            if doc is None or doc.id in seen_doc_ids:
                continue
            seen_doc_ids.add(doc.id)
            result: dict[str, Any] = {
                "doc_id": doc.id,
                "citation": doc.citation,
            }
            score = hit.get("score")
            if score is not None:
                result["score"] = float(score)
            mapped_results.append(result)

        status = "ok" if not error_code else "partial"
        row = {
            "query_id": query.id,
            "status": status,
            "latency_ms": round(latency_ms, 2),
            "results": mapped_results,
        }
        if error_code:
            row["error_code"] = error_code
        query_rows.append(row)

    return {
        "schema_version": "mem.recall-rankings.v1",
        "engine": engine_label,
        "configuration": {
            "mode": mode,
            "provider": provider,
            "model": model,
            "dimension": dimension,
            "index": {
                "kind": "pgvector",
                "distance": "cosine",
            },
            "search": {
                "top_k": limit,
                "type": "auto",
            },
        },
        "hardware": {
            "host": _coarse_host(),
            "client": socket.gethostname(),
        },
        "queries": query_rows,
    }


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python -m benchmarks.recall.produce",
        description="Query a live memd and emit mem.recall-rankings.v1.",
    )
    parser.add_argument("--memd-url", required=True, help="base URL of memd")
    parser.add_argument("--token", required=True, help="bearer token for auth")
    parser.add_argument(
        "--dataset", type=Path, default=DEFAULT_DATASET, help="dataset directory"
    )
    parser.add_argument(
        "--output", type=Path, required=True, help="output rankings JSON path"
    )
    parser.add_argument(
        "--limit", type=int, default=10, help="max results per query (default 10)"
    )
    parser.add_argument(
        "--timeout", type=float, default=30.0, help="per-query timeout in seconds"
    )
    parser.add_argument(
        "--engine",
        default="live-memd",
        help="engine label for the artifact (default: live-memd)",
    )
    parser.add_argument(
        "--dimension",
        type=int,
        default=1536,
        help="embedding dimension of the live model (default: 1536)",
    )
    parser.add_argument(
        "--mode",
        default="hybrid",
        choices=["lexical", "vector", "hybrid"],
        help="search mode (default: hybrid)",
    )
    parser.add_argument(
        "--provider",
        default="memd",
        help="provider label (default: memd)",
    )
    parser.add_argument(
        "--model",
        default="memd-embedded",
        help="model label (default: memd-embedded)",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    dataset = load_dataset(args.dataset)
    rankings = produce_rankings(
        dataset,
        base_url=args.memd_url,
        token=args.token,
        limit=args.limit,
        timeout=args.timeout,
        engine_label=args.engine,
        dimension=args.dimension,
        mode=args.mode,
        provider=args.provider,
        model=args.model,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(
        json.dumps(rankings, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    ok_count = sum(1 for q in rankings["queries"] if q["status"] == "ok")
    err_count = sum(1 for q in rankings["queries"] if q["status"] == "error")
    print(f"wrote {args.output} ({ok_count} ok, {err_count} error)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
