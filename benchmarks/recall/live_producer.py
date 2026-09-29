"""Produce mem.recall-rankings.v1 from a live memd instance.

Queries each dataset query against POST /v1/search, maps API results back to
dataset doc_ids by path, and emits the rankings JSON that the existing harness
consumes via --rankings.
"""

from __future__ import annotations

import json
import math
import platform
import posixpath
import time
import unicodedata
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

from .dataset import Dataset, Document
from .errors import BenchmarkError

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
    # A snippet cannot establish tenant identity. Never choose an authorized
    # document merely because a foreign document shares its path or words.
    if len({doc.workspace for doc in candidates}) != 1:
        return None
    normalized_snippet = unicodedata.normalize("NFKC", snippet).casefold()
    best: Document | None = None
    best_overlap = 0
    for doc in candidates:
        doc_tokens = set(unicodedata.normalize("NFKC", doc.text).casefold().split())
        overlap = sum(1 for t in normalized_snippet.split() if t in doc_tokens)
        if overlap > best_overlap:
            best_overlap = overlap
            best = doc
        elif overlap == best_overlap:
            best = None
    return best


def _source_kind_to_api_type(source_kind: str) -> str | None:
    return _SOURCE_KIND_TO_TYPE.get(source_kind)


def _coarse_host() -> str:
    """Record OS/architecture only, never a hostname or client identity."""
    try:
        return f"{platform.system()}/{platform.machine()}"
    except Exception:
        return "unknown"


def _json_get(
    base_url: str,
    path: str,
    token: str,
    *,
    timeout: float,
) -> dict[str, Any]:
    url = base_url.rstrip("/") + path
    req = Request(url, method="GET")
    req.add_header("Authorization", f"Bearer {token}")
    try:
        with urlopen(req, timeout=timeout) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except HTTPError as exc:
        raise BenchmarkError(f"GET {path} failed with HTTP {exc.code}") from exc
    except URLError as exc:
        raise BenchmarkError(f"GET {path} failed: {exc.reason}") from exc
    except Exception as exc:
        raise BenchmarkError(f"GET {path} returned unreadable JSON") from exc
    if not isinstance(payload, dict):
        raise BenchmarkError(f"GET {path} must return a JSON object")
    return payload


def discover_live_configuration(
    base_url: str,
    token: str,
    *,
    mode: str,
    timeout: float = 30.0,
) -> dict[str, Any]:
    """Read engine/profile identity from a running memd, not from CLI flags."""
    if mode not in {"lexical", "vector"}:
        raise BenchmarkError("memd /v1/search does not expose a hybrid lexical/vector route")
    version = _json_get(base_url, "/v1/version", token, timeout=timeout)
    version_label = version.get("version")
    revision = version.get("revision")
    contract = version.get("contract")
    if not isinstance(version_label, str) or not version_label.strip():
        raise BenchmarkError("GET /v1/version did not return a version string")
    engine = f"memd/{version_label.strip()}"
    if engine == "lexical-reference":
        raise BenchmarkError("engine must identify live memd, not lexical-reference")

    profile_payload = _json_get(
        base_url, "/v1/workspaces/current/ai-profile", token, timeout=timeout
    )
    active = profile_payload.get("active")
    server: dict[str, Any] = {
        "version": version_label.strip(),
        "revision": revision if isinstance(revision, str) else None,
        "contract": contract if isinstance(contract, str) else None,
    }
    index = {
        "kind": "not-advertised",
        "evidence": (
            "GET /v1/version and GET /v1/workspaces/current/ai-profile do not "
            "expose ANN identity"
        ),
    }
    evidence = (
        "discovered from GET /v1/version and GET /v1/workspaces/current/ai-profile"
    )
    if mode == "lexical":
        return {
            "engine": engine,
            "provider": None,
            "model": None,
            "dimension": None,
            "index": index,
            "server": server,
            "evidence": evidence,
        }
    if not isinstance(active, dict):
        raise BenchmarkError(
            "vector produce requires an active workspace AI profile from the running memd"
        )
    embedding = active.get("embedding")
    if not isinstance(embedding, dict):
        raise BenchmarkError("active AI profile is missing embedding stage")
    provider = embedding.get("provider")
    dimension = embedding.get("dimensions")
    profile_id = active.get("profile_id")
    profile_revision = active.get("profile_revision")
    if not isinstance(provider, str) or not provider.strip():
        raise BenchmarkError("active AI profile embedding.provider is empty")
    if not isinstance(dimension, int) or isinstance(dimension, bool) or dimension <= 0:
        raise BenchmarkError("active AI profile embedding.dimensions must be a positive integer")
    if not isinstance(profile_id, str) or not profile_id.strip():
        raise BenchmarkError("active AI profile_id is empty")
    model = profile_id.strip()
    if isinstance(profile_revision, str) and profile_revision.strip():
        model = f"{model}@{profile_revision.strip()}"
    server["profile_id"] = profile_id.strip()
    if isinstance(profile_revision, str) and profile_revision.strip():
        server["profile_revision"] = profile_revision.strip()
    return {
        "engine": engine,
        "provider": provider.strip(),
        "model": model,
        "dimension": dimension,
        "index": index,
        "server": server,
        "evidence": evidence,
    }


def _query_memd(
    base_url: str,
    token: str,
    query_text: str,
    *,
    scope: str = "",
    type_filter: str = "",
    route: str = "auto",
    limit: int = 10,
    timeout: float = 30.0,
) -> tuple[list[dict[str, Any]], float, str | None]:
    body: dict[str, Any] = {"query": query_text, "limit": limit, "route": route}
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
        if not isinstance(payload, dict) or "results" not in payload:
            return [], elapsed_ms, "invalid_response"
        results = payload["results"]
        if results is None:
            results = []  # memd encodes an empty nil hit slice as null.
        if not isinstance(results, list) or any(not isinstance(hit, dict) for hit in results):
            return [], elapsed_ms, "invalid_response"
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
    dimension: int = 768,
    mode: str = "vector",
    provider: str = "operator-unspecified",
    model: str = "operator-unspecified",
    live_config: dict[str, Any] | None = None,
) -> dict[str, Any]:
    if mode not in {"lexical", "vector"}:
        raise BenchmarkError("memd /v1/search does not expose a hybrid lexical/vector route")
    if not 1 <= limit <= 100 or not math.isfinite(timeout) or timeout <= 0:
        raise BenchmarkError("limit must be 1..100 and timeout must be positive and finite")
    index_config: dict[str, Any] = {"kind": "operator-unspecified"}
    evidence = "operator-declared configuration; model and index not verified by producer"
    server_identity: dict[str, Any] | None = None
    if live_config is not None:
        engine_label = live_config["engine"]
        provider = live_config["provider"]
        model = live_config["model"]
        dimension = live_config["dimension"]
        index_config = dict(live_config["index"])
        evidence = live_config["evidence"]
        server_identity = live_config.get("server")
    if not isinstance(engine_label, str) or not engine_label.strip() or engine_label == "lexical-reference":
        raise BenchmarkError("engine must identify live memd, not lexical-reference")
    if mode == "vector" and (
        not isinstance(dimension, int) or isinstance(dimension, bool) or dimension <= 0
        or not isinstance(provider, str) or not provider.strip()
        or not isinstance(model, str) or not model.strip()
    ):
        raise BenchmarkError("vector mode requires a positive dimension and non-empty provider/model labels")
    path_index = _build_path_index(list(dataset.documents))

    query_rows: list[dict[str, Any]] = []
    for query in dataset.queries:
        if query.expected_source_kind == "structured":
            query_rows.append({"query_id": query.id, "status": "error",
                               "latency_ms": 0.0, "results": [],
                               "error_code": "unsupported_source_kind"})
            continue
        if query.filters.get("metadata"):
            query_rows.append({"query_id": query.id, "status": "error",
                               "latency_ms": 0.0, "results": [],
                               "error_code": "unsupported_filter"})
            continue
        scope = query.filters.get("path_prefix", "")
        type_filter = _source_kind_to_api_type(query.expected_source_kind) or ""

        api_results, latency_ms, error_code = _query_memd(
            base_url,
            token,
            query.text,
            scope=scope,
            type_filter=type_filter,
            route="lexical" if mode == "lexical" else "text",
            limit=limit,
            timeout=timeout,
        )

        if error_code:
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
        mapping_error: str | None = None
        for hit in api_results:
            hit_path = hit.get("path")
            name = hit.get("name", "")
            snippet = hit.get("snippet", "")
            score = hit.get("score")
            # Validate every row before deduplication: a malformed duplicate is
            # still evidence of a failed response, not something to discard.
            if (
                not isinstance(hit_path, str) or not hit_path.startswith("/")
                or not isinstance(name, str) or not isinstance(snippet, str)
                or (name and ("/" in name or name in {".", ".."}))
                or (score is not None and (
                    isinstance(score, bool) or not isinstance(score, (int, float))
                    or not math.isfinite(score)
                ))
            ):
                mapping_error = "invalid_result"
                break
            if name:
                # memd returns a folder path and file name separately.
                hit_path = posixpath.join(hit_path, name)
            candidates = path_index.get(hit_path, [])
            doc = _match_doc_by_path(hit_path, snippet, candidates)
            if doc is None:
                mapping_error = "unmapped_result"
                break
            if doc.id in seen_doc_ids:
                continue
            seen_doc_ids.add(doc.id)
            result: dict[str, Any] = {
                "doc_id": doc.id,
                "citation": doc.citation,
            }
            if score is not None:
                result["score"] = float(score)
            mapped_results.append(result)

        if mapping_error:
            mapped_results = []
        status = "ok" if not mapping_error else "error"
        row = {
            "query_id": query.id,
            "status": status,
            "latency_ms": round(latency_ms, 2),
            "results": mapped_results,
        }
        if mapping_error:
            row["error_code"] = mapping_error
        query_rows.append(row)

    configuration: dict[str, Any] = {
        "mode": mode,
        "provider": None if mode == "lexical" else provider,
        "model": None if mode == "lexical" else model,
        "dimension": None if mode == "lexical" else dimension,
        "evidence": evidence,
        "index": index_config,
        "search": {
            "top_k": limit,
            "route": "lexical" if mode == "lexical" else "text",
            "workspace": "bound by the supplied token; not inferred from dataset labels",
        },
    }
    if isinstance(server_identity, dict) and server_identity:
        configuration["server"] = server_identity
    return {
        "schema_version": "mem.recall-rankings.v1",
        "engine": engine_label,
        "configuration": configuration,
        "hardware": {
            "host": _coarse_host(),
        },
        "queries": query_rows,
    }
