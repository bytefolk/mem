#!/usr/bin/env python3
"""Exercise native memd config and Worker probes on the built runtime images."""
from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import re
import subprocess
import time
import uuid


REPO = Path(__file__).resolve().parent.parent


def run(*args: str, check: bool = True, timeout: int = 45) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    if check and result.returncode:
        raise RuntimeError(f"test command failed: {args[0]}")
    return result


def native_workload(text: str, suffix: str) -> str:
    name = "  name: ${{ defaults.app_name }}" + suffix + "\n"
    matches = [doc for doc in text.split("\n---\n") if "kind: StatefulSet\n" in doc and name in doc]
    if len(matches) != 1:
        raise AssertionError(f"expected one native {suffix} StatefulSet")
    return matches[0]


def inline_env(workload: str, name: str) -> str:
    values = re.findall(r"name: " + re.escape(name) + r", value: ([^}]+?) \}", workload)
    if len(values) != 1:
        raise AssertionError(f"expected one inline environment value for {name}")
    return values[0].strip().strip("'\"")


def worker_probe(workload: str, name: str) -> dict[str, int]:
    matches = re.findall(r"(?m)^          " + re.escape(name) + r":\n((?:            .+\n)+)", workload)
    if len(matches) != 1 or "exec: { command: [python, -m, mem_worker.healthcheck] }" not in matches[0]:
        raise AssertionError(f"expected native Worker {name} health command")
    return {key: int(value) for key, value in re.findall(r"(timeoutSeconds|periodSeconds|failureThreshold): ([0-9]+)", matches[0])}


def main() -> None:
    text = (REPO / "deploy/sealos/template.yaml").read_text(encoding="utf-8")
    memd = native_workload(text, "-memd")
    worker = native_workload(text, "-worker")
    probes = {name: worker_probe(worker, name) for name in ("startupProbe", "readinessProbe", "livenessProbe")}
    # healthcheck permits two consecutive 3-second gRPC waits plus Python startup.
    for name, probe in probes.items():
        if probe.get("timeoutSeconds", 1) < 10:
            raise AssertionError(f"{name} cannot accommodate the real healthcheck")
    if probes["startupProbe"].get("periodSeconds", 10) * probes["startupProbe"].get("failureThreshold", 3) < 120:
        raise AssertionError("Worker startup budget must allow cold imports")
    key = base64.b64encode(b"x" * 32).decode()
    assert len(base64.b64decode(key)) == 32
    env = {name: inline_env(memd, name) for name in (
        "MEM_RUNTIME_PROFILE", "MEM_AUTO_MIGRATE", "MEM_DEPLOYMENT_MODE", "MEM_REGISTRATION_MODE",
        "MEM_GITHUB_BOOTSTRAP", "MEM_AI_PROFILES", "MEM_SESSION_TTL", "MEM_WORKER_AUTH_KEY_ID",
        "MEM_WORKSPACE_TRANSFER_TMP_DIR", "MEM_WORKSPACE_BUNDLE_MAX_BYTES", "MEM_WORKSPACE_TRANSFER_MAX_CONCURRENT",
    )}
    env.update({"MEM_DB_URL": "postgres://fixture:fixture@127.0.0.1:15432/fixture?sslmode=disable",
                "MEM_REDIS_URL": "redis://127.0.0.1:16379/0", "MEM_S3_ENDPOINT": "https://storage.example.invalid",
                "MEM_S3_ACCESS_KEY": "fixture-access", "MEM_S3_SECRET_KEY": "fixture-secret", "MEM_S3_BUCKET": "fixture",
                "MEM_WORKER_GRPC": "127.0.0.1:50051", "MEM_WORKER_AUTH_KEY_B64": key,
                "MEM_PUBLIC_URL": "https://preview.example.invalid", "MEM_GITHUB_CLIENT_ID": "fixture-client",
                "MEM_GITHUB_CLIENT_SECRET": "fixture-secret", "MEM_GITHUB_ALLOWED_USER_IDS": "1"})
    server_image = os.environ.get("MEM_SEALOS_SERVER_TEST_IMAGE", "mem-server:deploy-validation")
    worker_image = os.environ.get("MEM_SEALOS_WORKER_TEST_IMAGE", "mem-worker:deploy-validation")
    redis_image = os.environ.get("MEM_SEALOS_REDIS_TEST_IMAGE", "redis:7.4.2-alpine")
    owned = "mem-native-runtime-" + uuid.uuid4().hex[:12]
    server_name = owned + "-memd"
    argv = ["docker", "run", "--rm", "--name", server_name, "--network", "none", "--read-only", "--user", "65532:65532",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--cpus", "1", "--memory", "512m",
            "--tmpfs", "/tmp:rw,uid=65532,gid=65532,mode=1777"]
    for name, value in env.items():
        argv.extend(["--env", name + "=" + value])
    try:
        result = run(*argv, server_image, check=False)
        startup_log = result.stdout + result.stderr
        if result.returncode != 1 or "db open:" not in startup_log or "config:" in startup_log or "workspace transfer temp dir:" in startup_log:
            raise AssertionError("native memd recipe must pass config and private tmp setup before isolated DB refusal")
    finally:
        run("docker", "rm", "--force", server_name, check=False)
    print("PASS: native memd production config and 0700 transfer directory on the actual image")

    redis_name, worker_name = owned + "-redis", owned + "-worker"
    created: list[str] = []
    network = False
    try:
        run("docker", "network", "create", "--internal", owned)
        network = True
        run("docker", "run", "--detach", "--rm", "--name", redis_name, "--network", owned,
            "--label", "com.bytefolk.mem.test=sealos-runtime", redis_image)
        created.append(redis_name)
        run("docker", "run", "--detach", "--rm", "--name", worker_name, "--network", owned,
            "--label", "com.bytefolk.mem.test=sealos-runtime", "--read-only", "--user", "1000:1000",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--cpus", "0.5", "--memory", "512m",
            "--tmpfs", "/tmp:rw,uid=1000,gid=1000,mode=1777",
            "--env", "MEM_WORKER_AUTH_MODE=" + inline_env(worker, "MEM_WORKER_AUTH_MODE"),
            "--env", "MEM_WORKER_AUTH_KEY_ID=" + inline_env(worker, "MEM_WORKER_AUTH_KEY_ID"),
            "--env", "MEM_WORKER_AUTH_KEY_B64=" + key,
            "--env", "MEM_WORKER_AUTH_REPLAY_REDIS_URL=redis://" + redis_name + ":6379/1",
            worker_image)
        created.append(worker_name)
        startup_budget = probes["startupProbe"]["periodSeconds"] * probes["startupProbe"]["failureThreshold"]
        deadline = time.monotonic() + startup_budget
        while True:
            started = time.monotonic()
            try:
                result = run("docker", "exec", worker_name, "python", "-m", "mem_worker.healthcheck", check=False,
                             timeout=probes["startupProbe"]["timeoutSeconds"])
            except subprocess.TimeoutExpired:
                result = None
            if result is not None and result.returncode == 0:
                break
            if time.monotonic() >= deadline:
                raise AssertionError("native authenticated Worker startup healthcheck failed")
            time.sleep(min(max(0, probes["startupProbe"]["periodSeconds"] - (time.monotonic() - started)), max(0, deadline - time.monotonic())))
        for name in ("readinessProbe", "livenessProbe"):
            run("docker", "exec", worker_name, "python", "-m", "mem_worker.healthcheck", timeout=probes[name]["timeoutSeconds"])
        state = json.loads(run("docker", "inspect", "--format", "{{json .State}}", worker_name).stdout)
        if not state["Running"] or state.get("OOMKilled"):
            raise AssertionError("Worker did not remain healthy at native CPU/memory limits")
        print("PASS: native Worker startup/readiness/liveness with required auth, 500m CPU and read-only root")
    finally:
        for name in reversed(created):
            run("docker", "rm", "--force", name, check=False)
        if network:
            run("docker", "network", "rm", owned, check=False)


if __name__ == "__main__":
    main()
