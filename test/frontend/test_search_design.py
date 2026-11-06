"""Validate the isolated search design UI, SQLite constraints and dark Mermaid rendering."""
import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path
import re
import sqlite3
import subprocess
import sys
import tempfile
from uuid import uuid4

sys.dont_write_bytecode = True
from fixtures import ROOT, available_port, stop_process, wait_ready

DESIGN = ROOT / "aidd-wiki/_pending/design-search-20261106"


def validate_schema():
    """Exercise DDL guards and CAS primitives using disposable in-memory SQLite, never user databases."""
    with contextlib.closing(sqlite3.connect(":memory:")) as connection:
        connection.execute("PRAGMA foreign_keys=ON")
        connection.executescript("CREATE TABLE projects(id TEXT PRIMARY KEY); CREATE TABLE integrations(id INTEGER PRIMARY KEY CHECK(id=1),document TEXT NOT NULL); INSERT INTO projects VALUES('p1'),('p2');")
        old = {"revision": 1, "providers": [{"id": "legacy", "kind": "wikipedia", "enabled": True}], "search_enabled": True, "hindsight_url": "http://127.0.0.1:8888"}
        connection.execute("INSERT INTO integrations VALUES(1,?)", (json.dumps(old),))
        connection.commit()
        ddl = (DESIGN / "ddl/schema.sql").read_text()
        connection.executescript("BEGIN IMMEDIATE;" + ddl + "COMMIT;")
        connection.execute("INSERT INTO search_settings VALUES(1,0,?)", (json.dumps({"schema_version": 1, "profiles": old["providers"]}),))
        shell = dict(old, providers=[], search_enabled=False)
        connection.execute("UPDATE integrations SET document=? WHERE id=1", (json.dumps(shell),))
        connection.commit()
        checks = []
        try:
            connection.execute("UPDATE integrations SET document=? WHERE id=1", (json.dumps(old),))
        except sqlite3.IntegrityError:
            checks.append("legacy search write rejected")
        else:
            raise AssertionError("Legacy metadata bypassed the upgraded search authority")
        shell["hindsight_url"] = "http://127.0.0.1:9999"
        connection.execute("UPDATE integrations SET document=? WHERE id=1", (json.dumps(shell),))
        checks.append("Hindsight-only metadata update allowed")
        assert connection.execute("UPDATE search_settings SET revision=revision+1 WHERE id=1 AND revision=0").rowcount == 1
        assert connection.execute("UPDATE search_settings SET revision=revision+1 WHERE id=1 AND revision=0").rowcount == 0
        checks.append("stale CAS update rejected")
        sql = "INSERT INTO search_runs(id,project_id,request_scope,request_id,payload_hash,profile_id,purpose,operation,status,execution_owner,document,created_at) VALUES(?,?,?,?,?,'profile','research','search',?,'owner',?,'now')"
        connection.execute(sql, ("r1", None, "global", "request", "hash", "queued", "{}"))
        assert connection.execute("UPDATE search_runs SET revision=1,status='running',document=? WHERE id='r1' AND revision=0", ('{"attempts_reserved":1}',)).rowcount == 1
        assert connection.execute("UPDATE search_runs SET revision=1,document=? WHERE id='r1' AND revision=0", ('{"attempts_reserved":0}',)).rowcount == 0
        checks.append("run CAS preserves a newer budget checkpoint")
        assert connection.execute("UPDATE search_runs SET revision=2,status='cancelled',finished_at='now' WHERE id='r1' AND revision=1").rowcount == 1
        assert connection.execute("UPDATE search_runs SET revision=2,status='succeeded' WHERE id='r1' AND revision=1").rowcount == 0
        checks.append("late completion cannot overwrite a newer cancellation")
        for row, description in [(("r2", None, "global", "request", "hash", "queued", "{}"), "duplicate global request rejected"), (("r3", None, "project:p1", "other", "hash", "queued", "{}"), "mismatched scope rejected"), (("r4", None, "global", "other", "hash", "invalid", "{}"), "invalid run state rejected")]:
            try:
                connection.execute(sql, row)
            except sqlite3.IntegrityError:
                checks.append(description)
            else:
                raise AssertionError(description)
        connection.execute(sql, ("p1-run", "p1", "project:p1", "request", "hash", "queued", "{}"))
        connection.execute(sql, ("p2-run", "p2", "project:p2", "request", "hash", "queued", "{}"))
        checks.append("same request ID isolated across project scopes")
        connection.execute("DELETE FROM projects WHERE id='p1'")
        assert connection.execute("SELECT COUNT(*) FROM search_runs WHERE id='p1-run'").fetchone()[0] == 0
        checks.append("project-owned run follows existing deletion scope")
        return checks


def run(review, result_path):
    """Build and test the prototype while cleaning temporary browser, server, cache, SVG and screenshots."""
    receipt = {"scope": "design-prototype-only", "result": "failed"}
    with tempfile.TemporaryDirectory(prefix="quillweave-search-design-") as directory:
        work = Path(directory)
        server = None
        cli = ["npx", "--yes", "--package=@playwright/cli@0.1.23", "playwright-cli", "--session", "search-design-" + uuid4().hex[:8]]
        log = (work / "server.log").open("w+")
        try:
            subprocess.run(["node", "node_modules/typescript/bin/tsc", "--noEmit", "-p", "aidd-wiki/output/fe-demo/tsconfig.json"], cwd=ROOT, check=True, timeout=120)
            receipt["sqlite_checks"] = validate_schema()
            inline = re.search(r"```mermaid\n(.*?)```", (DESIGN / "DES-search.md").read_text(), re.S).group(1).strip()
            standalone = "\n".join((DESIGN / "architecture.mmd").read_text().splitlines()[1:]).strip()
            assert inline == standalone
            (work / "puppeteer.json").write_text(json.dumps({"executablePath": "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}))
            subprocess.run(["mmdc", "-i", str(DESIGN / "architecture.mmd"), "-o", str(work / "architecture.png"), "-w", "1800", "-t", "dark", "-b", "#111827", "-p", str(work / "puppeteer.json")], check=True, capture_output=True, timeout=90)
            receipt["mermaid_dark_render"] = True
            port = available_port()
            environment = dict(os.environ, DESIGN_ROOT=str(ROOT), DESIGN_PORT=str(port), DESIGN_CACHE=str(work / "vite-cache"), DESIGN_BUILD=str(work / "dist"))
            source = """import {createServer,build} from 'vite';
// Compile to a disposable directory before serving the same source tree.
const common={root:process.env.DESIGN_ROOT,configFile:process.env.DESIGN_ROOT+'/frontend/vite.config.mjs',cacheDir:process.env.DESIGN_CACHE};
await build({...common,build:{outDir:process.env.DESIGN_BUILD,emptyOutDir:true,rollupOptions:{input:process.env.DESIGN_ROOT+'/aidd-wiki/output/fe-demo/index.html'}}});
const server=await createServer({...common,server:{host:'127.0.0.1',port:Number(process.env.DESIGN_PORT),strictPort:true,proxy:{},warmup:{clientFiles:[]}}});await server.listen();"""
            server = subprocess.Popen(["node", "--input-type=module", "-e", source], cwd=ROOT, env=environment, stdout=log, stderr=log, start_new_session=True)
            url = f"http://127.0.0.1:{port}/aidd-wiki/output/fe-demo/"
            wait_ready(url, server)
            subprocess.run(cli + ["open", url], cwd=work, check=True, capture_output=True, text=True, timeout=45)
            cases = (ROOT / "test/frontend/search_design_cases.js").read_text().replace("__DESIGN_ARTIFACTS__", json.dumps(str(work)))
            completed = subprocess.run(cli + ["run-code", cases], cwd=work, capture_output=True, text=True, timeout=180)
            if completed.returncode or "### Error" in completed.stdout:
                raise RuntimeError(completed.stdout[-4500:] + completed.stderr[-1000:])
            match = re.search(r"### Result\s*\n(.*?)(?=\n### |\Z)", completed.stdout, re.S)
            if not match:
                raise RuntimeError("Missing structured browser result: " + completed.stdout[-1000:])
            receipt["browser"] = json.loads(match.group(1))
            if receipt["browser"]["result"] != "passed" or len(receipt["browser"]["checks"]) != 10:
                raise AssertionError("The complete prototype acceptance did not run")
            receipt["result"] = "passed"
            print("SEARCH_DESIGN_PASSED: 10 UI groups, SQLite guards, dark Mermaid and isolated build", flush=True)
            if review:
                print("DESIGN_REVIEW_DIRECTORY " + str(work), flush=True)
                sys.stdin.readline()
        except BaseException:
            log.flush(); log.seek(0)
            print(log.read()[-2500:], flush=True)
            if review:
                print("FAILED_DESIGN_REVIEW_DIRECTORY " + str(work), flush=True)
                sys.stdin.readline()
            raise
        finally:
            try:
                subprocess.run(cli + ["close"], cwd=work, capture_output=True, timeout=15)
            except (OSError, subprocess.TimeoutExpired):
                pass
            if server is not None:
                stop_process(server)
            log.close()
            receipt["temporary_files_cleaned"] = True
            receipt["case_sha256"] = hashlib.sha256((ROOT / "test/frontend/search_design_cases.js").read_bytes()).hexdigest()
            if result_path:
                Path(result_path).write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n")
    print("Search design browser, services, build, screenshots and cache cleaned.", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--review", action="store_true")
    parser.add_argument("--result")
    options = parser.parse_args()
    run(options.review, options.result)
