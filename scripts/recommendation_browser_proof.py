#!/usr/bin/env python3
"""Run the recommendation browser proof against fresh integration databases.

Requires the local PostgreSQL service, installed JS dependencies and Chromium.
The existing pilot application/database is not used for feed mutations.
"""
import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parents[1]


def run():
    subprocess.run(['cargo', 'build', '--locked', '--manifest-path', 'services/recommendation/Cargo.toml',
                    '--bin', 'jansetu-recommendation'], cwd=ROOT, check=True)
    subprocess.run(['npm', 'run', 'build'], cwd=ROOT, check=True)
    directory = Path(tempfile.mkdtemp(prefix='jansetu-recommendation-proof-'))
    processes, files = [], []
    fixture = directory / 'fixture.json'
    def start(command, cwd, extra, name):
        log = (directory / (name+'.log')).open('w')
        files.append(log)
        proc = subprocess.Popen(command, cwd=cwd, env=dict(os.environ, **extra),
                                stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        processes.append(proc)
        return proc
    try:
        start([str(ROOT / 'services/recommendation/target/debug/jansetu-recommendation')], ROOT,
              {'JANSETU_RECOMMENDATION_ADDR': '127.0.0.1:15051'}, 'rust')
        api = start(['go', 'test', './internal/app', '-run', '^TestRecommendationBrowserFixture$',
                     '-count=1', '-timeout=6m'], ROOT / 'services/backend',
                    {'JANSETU_INTEGRATION': '1', 'JANSETU_RECOMMENDATION_BROWSER_FIXTURE': str(fixture),
                     'JANSETU_RECOMMENDATION_TEST_TARGET': '127.0.0.1:15051'}, 'backend')
        deadline = time.monotonic() + 45
        while not fixture.exists():
            if api.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('Isolated backend fixture did not start')
            time.sleep(.2)
        metadata = json.loads(fixture.read_text())
        start(['node', str(ROOT / 'node_modules/next/dist/bin/next'), 'start', '-p', '13100', '-H', '127.0.0.1'], ROOT / 'apps/web',
              {'JANSETU_API_URL': metadata['apiUrl'], 'JANSETU_WEB_ORIGIN': 'http://127.0.0.1:13100'}, 'web')
        deadline = time.monotonic() + 20
        while True:
            try:
                with urlopen('http://127.0.0.1:13100', timeout=1) as response:
                    if response.status == 200:
                        break
            except OSError:
                if time.monotonic() > deadline:
                    raise RuntimeError('Web preview did not start')
                time.sleep(.2)
        subprocess.run(['npx', 'playwright', 'test', 'tests/e2e/recommendations.spec.ts'], cwd=ROOT,
                       env=dict(os.environ, JANSETU_WEB_URL='http://127.0.0.1:13100',
                                JANSETU_RECOMMENDATION_BROWSER_PROOF='1'), check=True)
        Path(metadata['stopFile']).touch()
        if api.wait(timeout=15) != 0:
            raise RuntimeError('Fixture cleanup failed')
        print('PASS: isolated browser/BFF/Go/Rust/PostgreSQL recommendation proof')
    finally:
        # Allow the fixture's TestMain to remove its temporary databases first.
        Path(str(fixture)+'.stop').touch()
        if 'api' in locals() and api.poll() is None:
            try:
                api.wait(timeout=15)
            except subprocess.TimeoutExpired:
                pass
        for proc in reversed(processes):
            if proc.poll() is None:
                os.killpg(proc.pid, signal.SIGTERM)
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait()
        for log in files:
            log.close()
        print('Proof diagnostics: '+str(directory))


if __name__ == '__main__':
    run()
