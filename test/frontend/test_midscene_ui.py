"""Run real Midscene visual acceptance with temporary dependencies, services and data."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
from threading import Thread
import time

# Reuse the existing fixture without generating bytecode caches inside the workspace.
sys.dont_write_bytecode = True
from fixtures import ROOT, FixtureServer, available_port, command, stop_process, wait_ready

MIDSCENE_VERSION = "1.17.6"
PLAYWRIGHT_VERSION = "1.65.0"


def terminate_group(process):
    """Release only this test's worker group, including an SDK-owned model transport after parent exit."""
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()


def run(timeout, result_path, review_seconds, credentials_only=False, search_only=False):
    """Execute screenshot-based actions against the real isolated app and clean on every exit path."""
    if not all(shutil.which(name) for name in ["npm", "node", "go"]):
        raise RuntimeError("Node.js, npm and Go are required")
    if not os.environ.get("MIDSCENE_MODEL_BASE_URL") and not shutil.which("codex"):
        raise RuntimeError("Configure a Midscene vision model or an authenticated Codex CLI")
    receipt = {"started_at": datetime.now(timezone.utc).isoformat(), "result": "failed",
               "midscene": MIDSCENE_VERSION, "playwright": PLAYWRIGHT_VERSION,
               "cases": [], "scope": "search" if search_only else "credentials" if credentials_only else "studio", "fixture": "local model/search boundary; real Go and SQLite"}
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="quillweave-midscene-") as temporary:
        work = Path(temporary)
        services = []
        worker = None
        relay = None
        worker_lines = []
        fixture = FixtureServer()
        Thread(target=fixture.serve_forever, daemon=True).start()
        log = (work / "services.log").open("w+")
        try:
            # The packages are testing tools; no production dependency or root lockfile changes.
            (work / "package.json").write_text(json.dumps({"name": "quillweave-midscene-acceptance", "private": True, "type": "module"}))
            environment = dict(os.environ, PUPPETEER_SKIP_DOWNLOAD="true", PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD="1")
            install = ["npm", "install", "--no-audit", "--no-fund", "--legacy-peer-deps", "--fetch-timeout=30000", "--fetch-retries=1",
                       f"@midscene/web@{MIDSCENE_VERSION}", f"playwright@{PLAYWRIGHT_VERSION}", f"@playwright/test@{PLAYWRIGHT_VERSION}"]
            print("Preparing pinned Midscene tools in a disposable directory", flush=True)
            subprocess.run(install, cwd=work, env=environment, check=True, timeout=240)
            lock = work / "package-lock.json"
            receipt["tool_lock_sha256"] = hashlib.sha256(lock.read_bytes()).hexdigest()
            backend = f"http://127.0.0.1:{available_port()}"
            frontend = f"http://127.0.0.1:{available_port()}"
            mock = f"http://127.0.0.1:{fixture.server_port}"
            binary = work / "preview"
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/preview"], cwd=ROOT, check=True)
            service_environment = dict(os.environ, QUILLWEAVE_DATA_DIR=str(work / "data"), QUILLWEAVE_PORT=backend.rsplit(":", 1)[1], QUILLWEAVE_API_KEY="")
            services.append(subprocess.Popen([str(binary)], cwd=ROOT, env=service_environment, stdout=log, stderr=log, start_new_session=True))
            vite = """import {createServer} from 'vite';
// Isolate build cache and preserve the preview server's origin checks through the dev proxy.
const server=await createServer({root:process.env.STUDIO_FRONTEND_ROOT,configFile:process.env.STUDIO_CONFIG,cacheDir:process.env.STUDIO_CACHE,server:{host:'127.0.0.1',port:Number(process.env.STUDIO_FRONTEND_PORT),strictPort:true,proxy:{'/api':{target:process.env.STUDIO_BACKEND,changeOrigin:true,configure(proxy){proxy.on('proxyReq',request=>request.setHeader('Origin',process.env.STUDIO_BACKEND));}}}}});await server.listen();"""
            service_environment.update(STUDIO_FRONTEND_ROOT=str(ROOT / "frontend"), STUDIO_CONFIG=str(ROOT / "frontend/vite.config.mjs"),
                                       STUDIO_CACHE=str(work / "node_modules/.vite"), STUDIO_FRONTEND_PORT=frontend.rsplit(":", 1)[1], STUDIO_BACKEND=backend)
            services.append(subprocess.Popen(["node", "--input-type=module", "-e", vite], cwd=ROOT, env=service_environment, stdout=log, stderr=log, start_new_session=True))
            wait_ready(backend, services[0])
            wait_ready(frontend, services[1])
            settings = command(backend, "get_model_settings", {})["settings"]
            profile = settings["profiles"][0]
            profile["enabled"], profile["name"] = True, "本地创作响应夹具"
            profile["config"].update(protocol="openai", provider_id="", base_url=mock + "/v1", model="studio-fixture", context_chars=24000, max_tokens=4096, timeout_seconds=20)
            settings["default_profile"] = profile["id"]
            command(backend, "save_model_settings", {"settings": settings, "key_updates": {profile["id"]: {"action": "clear"}}})
            environment.update(QUILLWEAVE_TEST_URL=frontend, QUILLWEAVE_TEST_MOCK=mock, MIDSCENE_RUN_DIR=str(work / "midscene_run"))
            environment["QUILLWEAVE_CREDENTIALS_ONLY"] = "1" if credentials_only else "0"
            environment["QUILLWEAVE_SEARCH_ONLY"] = "1" if search_only else "0"
            if not environment.get("MIDSCENE_MODEL_BASE_URL"):
                environment.update(MIDSCENE_MODEL_BASE_URL="codex://app-server", MIDSCENE_MODEL_NAME="gpt-6-sol", MIDSCENE_MODEL_FAMILY="gpt-6")
            environment.setdefault("MIDSCENE_MODEL_REASONING_ENABLED", "true")
            environment.setdefault("MIDSCENE_MODEL_REASONING_EFFORT", "low")
            environment.setdefault("MIDSCENE_MODEL_TIMEOUT", "180000")
            receipt["vision_model"] = environment.get("MIDSCENE_MODEL_NAME", "configured model")
            receipt["vision_transport"] = "codex app-server" if environment["MIDSCENE_MODEL_BASE_URL"].startswith("codex://") else "configured API"
            shutil.copyfile(ROOT / "test/frontend/midscene_ui_cases.mjs", work / "cases.mjs")
            receipt["case_source_sha256"] = hashlib.sha256((work / "cases.mjs").read_bytes()).hexdigest()
            worker = subprocess.Popen(["node", str(work / "cases.mjs")], cwd=work, env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, start_new_session=True)

            def relay_worker_output():
                """Keep bounded failure diagnostics while streaming only test-owned progress messages."""
                for line in worker.stdout:
                    worker_lines.append(line[:2000])
                    del worker_lines[:-100]
                    if line.startswith(("CASE ", "CASE_RESULT ", "MIDSCENE ", "MIDSCENE_ACCEPTANCE")):
                        print(line.rstrip(), flush=True)

            relay = Thread(target=relay_worker_output, daemon=True)
            relay.start()
            code = worker.wait(timeout=timeout)
            relay.join(timeout=2)
            if (work / "results.json").is_file():
                receipt.update(json.loads((work / "results.json").read_text()))
            if code:
                raise RuntimeError(f"Midscene worker exited {code}: " + "".join(worker_lines)[-2200:])
            requests = [r for r in fixture.requests if "messages" in r["body"]]
            if search_only:
                if [case.get("id") for case in receipt["cases"]] != ["S01", "S02", "S03"] or any(case.get("status") != "passed" for case in receipt["cases"]) or receipt.get("midscene_operations", 0) == 0:
                    raise AssertionError("Search visual acceptance did not execute its cases")
                if not any(r["body"].get("tools") for r in fixture.requests):
                    raise AssertionError("Visual native search never reached its real adapter")
            elif credentials_only:
                if [case.get("id") for case in receipt["cases"]] != ["K01"] or any(case.get("status") != "passed" for case in receipt["cases"]) or receipt.get("midscene_operations", 0) == 0:
                    raise AssertionError("Focused credential review did not execute its visual case")
                view = command(backend, "get_model_settings", {})
                if any(view["key_status"].values()):
                    raise AssertionError("Visual clear did not reach the real settings service")
            else:
                stories = command(backend, "list_projects", {})
                if len(stories) != 2:
                    raise AssertionError("Original and exactly one restored work must coexist")
                for story in stories:
                    project = command(backend, "get_project", {"project_id": story["id"]})
                    if project["studio"]["target_chapters"] != 500 or not project["chapters"] or not project["sources"] or not project["studio"]["memories"]:
                        raise AssertionError("The complete creation workflow was not persisted")
                if not requests or "简体中文" not in requests[0]["body"]["messages"][0]["content"]:
                    raise AssertionError("Chinese UI language did not reach the product model request")
            receipt["product_model_fixture_calls"] = len(requests)
            receipt["result"] = "passed"
        except BaseException as error:
            receipt["result"], receipt["error"] = "failed", str(error)[:2500]
            try:
                search_view = command(backend, "get_search_settings", {})
                receipt["search_profiles_at_failure"] = [{k: p.get(k) for k in ["id", "name", "family", "mode", "region", "enabled", "model_profile_id"]} for p in search_view["settings"]["profiles"]]
            except (OSError, RuntimeError, UnboundLocalError):
                pass
            if (work / "results.json").is_file():
                data = json.loads((work / "results.json").read_text())
                receipt["cases"] = data.get("cases", [])
                receipt["midscene_operations"] = data.get("midscene_operations", 0)
            if (work / "failure-context.json").is_file():
                receipt["diagnostic"] = json.loads((work / "failure-context.json").read_text())
            if review_seconds:
                print("MIDSCENE_FAILURE_REVIEW " + str(work), flush=True)
                time.sleep(min(60, review_seconds))
            log.flush(); log.seek(0)
            print(log.read()[-3000:], flush=True)
            raise
        finally:
            if worker is not None:
                terminate_group(worker)
            if relay is not None:
                relay.join(timeout=5)
            for process in reversed(services):
                stop_process(process)
            fixture.shutdown(); fixture.server_close(); log.close()
            receipt["seconds"] = round(time.monotonic() - started, 3)
            receipt["finished_at"] = datetime.now(timezone.utc).isoformat()
            receipt["temporary_files_cleaned"] = True
            if result_path:
                path = Path(result_path).resolve()
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n")
    print("MIDSCENE_RESULT " + json.dumps(receipt, ensure_ascii=False), flush=True)
    print("Midscene browser, model-transport group, temporary dependencies, reports, screenshots, services and database cleaned.", flush=True)


def main():
    """Expose a repeatable test command with an optional intentional text-only validation receipt."""
    parser = argparse.ArgumentParser()
    parser.add_argument("--timeout", type=int, default=1800)
    parser.add_argument("--result", help="Preserve only this explicit JSON receipt; all working artifacts are removed")
    parser.add_argument("--review-seconds", type=int, default=0, help="Allow up to 60 seconds to inspect a failed screenshot before automatic cleanup")
    parser.add_argument("--credentials-only", action="store_true", help="Run the direct key input visual case instead of the complete studio scenarios")
    parser.add_argument("--search-only", action="store_true", help="Run search configuration and native search visual acceptance")
    args = parser.parse_args()
    run(args.timeout, args.result, args.review_seconds, args.credentials_only, args.search_only)


if __name__ == "__main__":
    main()
