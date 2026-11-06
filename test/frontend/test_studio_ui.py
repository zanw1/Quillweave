"""Run the studio workflow and component acceptance in disposable services."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from threading import Thread
from uuid import uuid4

sys.dont_write_bytecode = True
from fixtures import ROOT, FixtureServer, available_port, command, stop_process, wait_ready


def run(review=False, search_only=False):
    """Build, test and clean up isolated services, browser state and all generated artifacts on every path."""
    session = "studio-" + uuid4().hex[:10]
    cli = ["npx", "--yes", "--package=@playwright/cli@0.1.23", "playwright-cli", "--session", session]
    with tempfile.TemporaryDirectory(prefix="quillweave-ui-test-") as temporary:
        work = Path(temporary)
        services = []
        fixture = FixtureServer()
        thread = Thread(target=fixture.serve_forever, daemon=True)
        thread.start()
        log = (work / "services.log").open("w+")
        try:
            backend = f"http://127.0.0.1:{available_port()}"
            frontend = f"http://127.0.0.1:{available_port()}"
            mock = f"http://127.0.0.1:{fixture.server_port}"
            binary = work / "preview"
            subprocess.run(["go", "build", "-o", str(binary), "./cmd/preview"], cwd=ROOT, check=True)
            environment = dict(os.environ, QUILLWEAVE_DATA_DIR=str(work / "data"), QUILLWEAVE_PORT=backend.rsplit(":", 1)[1], QUILLWEAVE_API_KEY="")
            services.append(subprocess.Popen([str(binary)], cwd=ROOT, env=environment, stdout=log, stderr=log, start_new_session=True))
            vite = """import {createServer} from 'vite';
// Keep all test cache and proxy traffic inside the disposable acceptance environment.
const server=await createServer({root:process.env.STUDIO_FRONTEND_ROOT,configFile:process.env.STUDIO_CONFIG,cacheDir:process.env.STUDIO_CACHE,server:{host:'127.0.0.1',port:Number(process.env.STUDIO_FRONTEND_PORT),strictPort:true,proxy:{'/api':{target:process.env.STUDIO_BACKEND,changeOrigin:true,configure(proxy){proxy.on('proxyReq',request=>request.setHeader('Origin',process.env.STUDIO_BACKEND));}}}}});await server.listen();"""
            environment.update(STUDIO_FRONTEND_ROOT=str(ROOT / "frontend"), STUDIO_CONFIG=str(ROOT / "frontend/vite.config.mjs"), STUDIO_CACHE=str(work / "node_modules/.vite"), STUDIO_FRONTEND_PORT=frontend.rsplit(":", 1)[1], STUDIO_BACKEND=backend)
            services.append(subprocess.Popen(["node", "--input-type=module", "-e", vite], cwd=ROOT, env=environment, stdout=log, stderr=log, start_new_session=True))
            wait_ready(backend, services[0])
            wait_ready(frontend, services[1])
            settings = command(backend, "get_model_settings", {})["settings"]
            profile = settings["profiles"][0]
            profile["name"] = "本地验收模型"
            profile["enabled"] = True
            profile["config"].update(protocol="openai", provider_id="", base_url=mock + "/v1", model="studio-fixture", context_chars=24000, max_tokens=4096, timeout_seconds=20)
            settings["default_profile"] = profile["id"]
            command(backend, "save_model_settings", {"settings": settings, "key_updates": {profile["id"]: {"action": "clear"}}})
            subprocess.run(cli + ["open", frontend], cwd=work, check=True, capture_output=True, text=True, timeout=45)
            case_file = "search_ui_cases.js" if search_only else "studio_ui_cases.js"
            source = (ROOT / "test/frontend" / case_file).read_text().replace("__STUDIO_MOCK_URL__", mock).replace("__STUDIO_ARTIFACT_DIR_JSON__", json.dumps(str(work)))
            completed = subprocess.run(cli + ["run-code", source], cwd=work, capture_output=True, text=True, timeout=180)
            if completed.returncode or "### Error" in completed.stdout:
                print(completed.stdout[-4000:], flush=True)
                diagnostic = subprocess.run(cli + ["eval", "() => document.body.innerText"], cwd=work, capture_output=True, text=True, timeout=20)
                print(diagnostic.stdout[-5000:], flush=True)
                print("Fixture model requests: " + str(len(fixture.requests)), flush=True)
                for story in command(backend, "list_projects", {}):
                    tasks = command(backend, "get_runs", {"project_id": story["id"]})
                    print("Task states: " + json.dumps([{key: task.get(key) for key in ["id", "action", "status", "error", "calls"]} for task in tasks]), flush=True)
                raise RuntimeError(completed.stderr or completed.stdout[-2500:])
            stories = command(backend, "list_projects", {})
            if search_only:
                section = completed.stdout.split("### Result\n", 1)[-1].split("### Ran Playwright code", 1)[0].strip()
                result = json.loads(section)
                if result.get("result") != "PASS: search settings and research" or len(result.get("checks", [])) < 8:
                    raise AssertionError("Search UI cases did not complete their final oracle")
                print(json.dumps(result, ensure_ascii=False), flush=True)
                return
            if len(stories) != 2:
                raise AssertionError("UI flow did not preserve the original and restored stories")
            project = command(backend, "get_project", {"project_id": stories[0]["id"]})
            if project["studio"]["target_chapters"] != 500 or not project["chapters"] or not project["sources"] or not project["studio"]["memories"]:
                raise AssertionError("UI actions did not persist the complete story workflow")
            model_requests = [request for request in fixture.requests if "messages" in request["body"]]
            if not model_requests or "简体中文" not in model_requests[0]["body"]["messages"][0]["content"]:
                raise AssertionError("Chinese interface language did not reach the real provider adapter")
            print(f"UI acceptance passed: complete studio workflow; {len(model_requests)} deterministic local model calls.", flush=True)
            if review:
                print("VISUAL_REVIEW_DIRECTORY " + temporary, flush=True)
                input("Press Enter after visual review; cleanup runs immediately afterwards.\n")
        except BaseException:
            log.flush()
            log.seek(0)
            print(log.read()[-5000:], flush=True)
            if review:
                print("FAILED_VISUAL_REVIEW_DIRECTORY " + temporary, flush=True)
                input("Agent diagnostic review; press Enter to clean the failed test environment.\n")
            raise
        finally:
            try:
                subprocess.run(cli + ["close"], cwd=work, capture_output=True, timeout=15)
            except (OSError, subprocess.TimeoutExpired):
                pass
            for process in reversed(services):
                stop_process(process)
            fixture.shutdown()
            fixture.server_close()
            log.close()
    print("UI browser, services, databases, cache and screenshots cleaned.", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--review", action="store_true", help="Keep the live test open for agent visual review before automatic cleanup")
    parser.add_argument("--search-only", action="store_true", help="Exercise actual search settings, execution and adoption")
    options = parser.parse_args()
    run(options.review, options.search_only)
