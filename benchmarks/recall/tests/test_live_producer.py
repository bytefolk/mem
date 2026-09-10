from __future__ import annotations

import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import tempfile
import threading
import unittest
from unittest.mock import patch

from benchmarks.recall.__main__ import main
from benchmarks.recall.adapters import load_external_rankings

from benchmarks.recall.live_producer import (
    _build_path_index,
    _match_doc_by_path,
    produce_rankings,
)
from benchmarks.recall.dataset import Document, load_dataset


class PathIndexTest(unittest.TestCase):
    def test_index_groups_by_path(self) -> None:
        docs = [
            Document(
                id="a", language="en", source_kind="text", workspace="alpha",
                path="/notes/a.md", citation="mem://files/a",
                text="alpha note", metadata={},
            ),
            Document(
                id="b", language="en", source_kind="text", workspace="alpha",
                path="/notes/a.md", citation="mem://files/b",
                text="beta note", metadata={},
            ),
        ]
        index = _build_path_index(docs)
        self.assertEqual(len(index["/notes/a.md"]), 2)

    def test_match_single_candidate(self) -> None:
        doc = Document(
            id="solo", language="en", source_kind="text", workspace="alpha",
            path="/notes/solo.md", citation="mem://files/solo",
            text="unique content", metadata={},
        )
        result = _match_doc_by_path("/notes/solo.md", "anything", [doc])
        self.assertEqual(result, doc)

    def test_match_picks_best_snippet_overlap(self) -> None:
        doc_a = Document(
            id="a", language="en", source_kind="text", workspace="alpha",
            path="/notes/shared.md", citation="mem://files/a",
            text="saturn ring observation", metadata={},
        )
        doc_b = Document(
            id="b", language="en", source_kind="text", workspace="alpha",
            path="/notes/shared.md", citation="mem://files/b",
            text="completely different topic", metadata={},
        )
        result = _match_doc_by_path("/notes/shared.md", "saturn ring", [doc_a, doc_b])
        self.assertEqual(result, doc_a)

    def test_ambiguous_path_does_not_guess_identity(self) -> None:
        docs = [Document(id=key, language="en", source_kind="text",
                         workspace=workspace, path="/shared.md", citation="mem://"+key,
                         text="same snippet", metadata={})
                for key, workspace in [("a", "alpha"), ("b", "beta")]]
        self.assertIsNone(_match_doc_by_path("/shared.md", "same snippet", docs))


class ProduceRankingsTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = Path(self.tempdir.name)
        self.dataset = self.root / "dataset"
        self.dataset.mkdir()
        (self.dataset / "dataset.json").write_text(
            json.dumps({
                "schema_version": "mem.recall-dataset.v1",
                "version": "unit-test-v1",
                "provenance": "hand-authored synthetic data",
                "license": "CC0-1.0",
                "required_coverage": {
                    "slices": ["exact"], "languages": ["en"], "source_kinds": ["text"],
                },
            }),
            encoding="utf-8",
        )
        (self.dataset / "corpus.jsonl").write_text(
            json.dumps({
                "id": "file-en-cassini", "language": "en", "source_kind": "text",
                "workspace": "alpha", "path": "/research/saturn.md",
                "citation": "mem://files/file-en-cassini",
                "text": "Cassini observed Saturn hexagonal storm",
                "provenance": "synthetic",
            }) + "\n",
            encoding="utf-8",
        )
        (self.dataset / "queries.jsonl").write_text(
            json.dumps({
                "id": "q-en-text-exact", "text": "Cassini Saturn hexagonal storm",
                "language": "en", "slice": "exact",
                "filters": {"workspace": "alpha", "source_kind": "text"},
                "expected_source_kind": "text",
            }) + "\n",
            encoding="utf-8",
        )
        (self.dataset / "qrels.json").write_text(
            json.dumps({"q-en-text-exact": {"file-en-cassini": 3}}),
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def test_http_fixture_runs_transport_and_emits_loadable_artifact(self) -> None:
        # Real loopback HTTP, synthetic response: this is not a live memd or
        # embedding-quality benchmark.
        requests = []

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                requests.append((self.path, json.loads(self.rfile.read(int(self.headers["Content-Length"])))))
                payload = json.dumps({"results": [{"path": "/research", "name": "saturn.md", "score": 0.9}]}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

            def log_message(self, *args):
                pass

        server = HTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        output = self.root / "http-fixture.json"
        try:
            code = main(["produce", "--memd-url", f"http://127.0.0.1:{server.server_port}",
                         "--token", "fixture", "--dataset", str(self.dataset),
                         "--output", str(output), "--provider", "fixture", "--model", "fixture"])
        finally:
            server.shutdown()
            thread.join()
            server.server_close()
        self.assertEqual(code, 0)
        self.assertEqual(requests[0][0], "/v1/search")
        self.assertEqual(requests[0][1]["route"], "text")
        artifact = load_external_rankings(output, load_dataset(self.dataset))
        self.assertEqual(artifact["queries"][0]["results"][0]["doc_id"], "file-en-cassini")
        self.assertGreater(artifact["queries"][0]["latency_ms"], 0)
        self.assertNotIn("client", artifact["hardware"])

    @patch("benchmarks.recall.live_producer._query_memd")
    def test_produce_rankings_success(self, mock_query: unittest.mock.MagicMock) -> None:
        mock_query.return_value = (
            [{"path": "/research/saturn.md", "snippet": "Cassini observed Saturn hexagonal storm", "score": 0.95}],
            12.5, None,
        )
        dataset = load_dataset(self.dataset)
        rankings = produce_rankings(
            dataset, base_url="http://localhost:8080", token="test-token", dimension=768,
        )
        self.assertEqual(rankings["schema_version"], "mem.recall-rankings.v1")
        self.assertEqual(rankings["engine"], "live-memd")
        self.assertEqual(rankings["configuration"]["dimension"], 768)
        self.assertEqual(len(rankings["queries"]), 1)
        query_row = rankings["queries"][0]
        self.assertEqual(query_row["query_id"], "q-en-text-exact")
        self.assertEqual(query_row["status"], "ok")
        self.assertGreater(query_row["latency_ms"], 0)
        self.assertEqual(len(query_row["results"]), 1)
        self.assertEqual(query_row["results"][0]["doc_id"], "file-en-cassini")

    @patch("benchmarks.recall.live_producer._query_memd")
    def test_produce_rankings_error(self, mock_query: unittest.mock.MagicMock) -> None:
        mock_query.return_value = ([], 5.0, "http_503")
        dataset = load_dataset(self.dataset)
        rankings = produce_rankings(
            dataset, base_url="http://localhost:8080", token="test-token",
        )
        query_row = rankings["queries"][0]
        self.assertEqual(query_row["status"], "error")
        self.assertEqual(query_row["error_code"], "http_503")
        self.assertEqual(query_row["results"], [])

    @patch("benchmarks.recall.live_producer._query_memd")
    def test_maps_shipping_folder_and_name_shape(self, mock_query) -> None:
        mock_query.return_value = ([{"path": "/research", "name": "saturn.md", "score": 0.9}], 1, None)
        rankings = produce_rankings(load_dataset(self.dataset), base_url="http://localhost", token="test")
        self.assertEqual(rankings["queries"][0]["results"][0]["doc_id"], "file-en-cassini")

    @patch("benchmarks.recall.live_producer._query_memd")
    def test_unmapped_hit_is_not_silently_discarded(self, mock_query) -> None:
        mock_query.return_value = ([{"path": "/foreign.md", "snippet": "private"}], 1, None)
        rankings = produce_rankings(load_dataset(self.dataset), base_url="http://localhost", token="test")
        self.assertEqual(rankings["queries"][0]["status"], "error")
        self.assertEqual(rankings["queries"][0]["error_code"], "unmapped_result")

    @patch("benchmarks.recall.live_producer._query_memd")
    def test_lexical_artifact_is_loadable_and_route_is_sent(self, mock_query) -> None:
        mock_query.return_value = ([], 1, None)
        dataset = load_dataset(self.dataset)
        rankings = produce_rankings(dataset, base_url="http://localhost", token="test", mode="lexical")
        output = self.root / "rankings.json"
        output.write_text(json.dumps(rankings), encoding="utf-8")
        load_external_rankings(output, dataset)
        self.assertEqual(mock_query.call_args.kwargs["route"], "lexical")

    @patch("benchmarks.recall.live_producer._query_memd")
    def test_produce_command_fails_when_requests_fail(self, mock_query) -> None:
        mock_query.return_value = ([], 1, "http_503")
        code = main(["produce", "--memd-url", "http://localhost", "--token", "test",
                     "--dataset", str(self.dataset), "--output", str(self.root / "failed.json")])
        self.assertEqual(code, 2)


if __name__ == "__main__":
    unittest.main()
