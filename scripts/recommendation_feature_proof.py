#!/usr/bin/env python3
"""Run parity proofs with a disposable Redis and isolated PostgreSQL databases."""
import os
from pathlib import Path
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parent.parent
IMAGE = "redis:8.2.3@sha256:0908d9af26bf9b985e984a40a5eb82eed229b07a3317eee6843832c3cc3a9619"


def main():
    container = "jansetu-feature-proof-" + uuid.uuid4().hex[:12]
    try:
        subprocess.run([
            "docker", "run", "-d", "--name", container, "--read-only",
            "--user", "999:999", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--memory", "128m", "--cpus", "1", "--tmpfs", "/tmp:rw,nosuid,size=16m",
            "-p", "127.0.0.1::6379", IMAGE, "redis-server", "--dir", "/tmp",
            "--save", "", "--appendonly", "no", "--maxmemory", "64mb", "--maxmemory-policy", "noeviction",
        ], check=True, stdout=subprocess.DEVNULL)
        port = subprocess.check_output([
            "docker", "inspect", "--format",
            '{{(index (index .NetworkSettings.Ports "6379/tcp") 0).HostPort}}', container,
        ], text=True).strip()
        for _ in range(50):
            probe = subprocess.run(["docker", "exec", container, "redis-cli", "ping"],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if probe.returncode == 0:
                break
            time.sleep(0.1)
        else:
            raise RuntimeError("isolated Redis did not become ready")
        environment = dict(os.environ, JANSETU_INTEGRATION="1",
                           JANSETU_FEATURE_PROOF_REDIS_URL=f"redis://127.0.0.1:{port}/0")
        subprocess.run(["go", "test", "-race", "./internal/app", "-run",
                        "TestRecommendationFeature", "-count=1"],
                       cwd=ROOT / "services/backend", env=environment, check=True)
        print("PASS: isolated PostgreSQL/Redis recommendation feature parity proof")
    finally:
        subprocess.run(["docker", "rm", "-f", container], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
