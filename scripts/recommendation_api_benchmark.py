#!/usr/bin/env python3
"""Measure an isolated synthetic Go HTTP / PostgreSQL / Rust feed workload."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import signal
import socket
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--requests", type=int, default=500)
    parser.add_argument("--concurrency", type=int, default=8)
    parser.add_argument("--ranker-max-in-flight", type=int, default=8)
    parser.add_argument("--authors", type=int, default=128)
    parser.add_argument("--posts-per-author", type=int, default=8)
    parser.add_argument("--output", type=Path, default=Path("/tmp/jansetu-recommendation-api-smoke.json"))
    args = parser.parse_args()
    for name, value, limit in (("requests", args.requests, 100000),
                               ("concurrency", args.concurrency, 128),
                               ("ranker-max-in-flight", args.ranker_max_in_flight, 128),
                               ("authors", args.authors, 1024),
                               ("posts-per-author", args.posts_per_author, 128)):
        if value < 1 or value > limit:
            parser.error(f"{name} must be between 1 and {limit}")
    if args.authors < 32:
        parser.error("at least 32 authors are needed for first and continuation pages")
    output = args.output.resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    # Every worker has a 10-second HTTP deadline. Allow the full bounded
    # request workload plus ten minutes for fixture setup and cleanup.
    timeout_seconds = 600 + 10 * ((args.requests + args.concurrency - 1) // args.concurrency)
    subprocess.run(["cargo", "build", "--locked", "--release", "--manifest-path",
                    "services/recommendation/Cargo.toml", "--bin", "jansetu-recommendation"],
                   cwd=ROOT, check=True)
    binary = ROOT / "services/recommendation/target/release/jansetu-recommendation"
    with tempfile.TemporaryDirectory(prefix="jansetu-api-benchmark-") as directory:
        raw_result = Path(directory) / "result.json"
        with socket.socket() as reservation:
            reservation.bind(("127.0.0.1", 0))
            port = reservation.getsockname()[1]
            address = f"127.0.0.1:{port}"
        with (Path(directory) / "rust.log").open("w") as log:
            ranker = subprocess.Popen([str(binary)], cwd=ROOT, start_new_session=True,
                                      env={"JANSETU_ENV": "test", "JANSETU_RECOMMENDATION_ADDR": address,
                                           "JANSETU_RECOMMENDATION_MAX_IN_FLIGHT": str(args.ranker_max_in_flight)},
                                      stdout=log, stderr=subprocess.STDOUT)
            try:
                deadline = time.monotonic() + 5
                while True:
                    try:
                        with socket.create_connection(("127.0.0.1", port), timeout=.2):
                            break
                    except OSError:
                        if ranker.poll() is not None or time.monotonic() > deadline:
                            raise RuntimeError("isolated Rust ranker did not start")
                        time.sleep(.05)
                environment = dict(os.environ, JANSETU_INTEGRATION="1",
                                   JANSETU_RECOMMENDATION_TARGET="",
                                   JANSETU_RECOMMENDATION_SNAPSHOT_REDIS_URL="",
                                   JANSETU_RECOMMENDATION_FEATURE_SHADOW_REDIS_URL="",
                                   JANSETU_RECOMMENDATION_TEST_TARGET=address,
                                   JANSETU_RECOMMENDATION_API_BENCHMARK_OUTPUT=str(raw_result),
                                   JANSETU_BENCHMARK_REQUESTS=str(args.requests),
                                   JANSETU_BENCHMARK_CONCURRENCY=str(args.concurrency),
                                   JANSETU_BENCHMARK_AUTHORS=str(args.authors),
                                   JANSETU_BENCHMARK_POSTS_PER_AUTHOR=str(args.posts_per_author))
                process = subprocess.run(["go", "test", "./internal/app", "-run",
                                          "^TestRecommendationAPIBenchmark$", "-count=1", f"-timeout={timeout_seconds}s"],
                                         cwd=ROOT / "services/backend", env=environment)
                if raw_result.exists():
                    result = json.loads(raw_result.read_text())
                    result.update({
                        "gitRevision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                        "worktreeDirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=ROOT, text=True).strip()),
                        "generatorSHA256": hashlib.sha256((ROOT / "services/backend/internal/app/recommendation_benchmark_integration_test.go").read_bytes()).hexdigest(),
                        "rustBinarySHA256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                        "rustBuild": "cargo build --locked --release, host process without a CPU or memory limit",
                        "rustMaxInFlight": args.ranker_max_in_flight,
                        "rustVersion": subprocess.check_output(["rustc", "--version"], text=True).strip(),
                        "hostArchitecture": platform.machine(), "hostLogicalCPUs": os.cpu_count(),
                        "hostMemoryBytes": os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES"),
                        "goRaceDetector": False,
                        "testTimeoutSeconds": timeout_seconds,
                    })
                    output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")
                    print(json.dumps({k: result[k] for k in ("requests", "errors", "successfulRequestsPerSecond", "allResponseLatency", "modeCounts")}))
                    print(f"Benchmark artifact: {output}")
                if process.returncode:
                    raise subprocess.CalledProcessError(process.returncode, process.args)
                if not raw_result.exists():
                    raise RuntimeError("benchmark completed without an artifact")
            finally:
                if ranker.poll() is None:
                    os.killpg(ranker.pid, signal.SIGTERM)
                    try:
                        ranker.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        os.killpg(ranker.pid, signal.SIGKILL)
                        ranker.wait()


if __name__ == "__main__":
    main()
