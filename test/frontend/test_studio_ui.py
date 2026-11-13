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


def verify_credential_restart(backend, mock, binary, environment, services, log, cli, work):
    """Rebuild and restart the isolated process, then verify encrypted secrets and the actual masked settings form."""
    models = command(backend, "get_model_settings", {})["settings"]
    model_id = models["profiles"][0]["id"]
    model_key = "BROWSER_RESTART_MODEL_KEY"
    search_key = "BROWSER_RESTART_SEARCH_KEY"
    saved = command(backend, "save_model_settings", {"settings": models, "key_updates": {model_id: {"action": "replace", "value": model_key}}})
    search = command(backend, "get_search_settings", {})
    presets = command(backend, "search_capabilities", {})["presets"]
    profile = next(item for item in presets if item["family"] == "tavily")
    profile.update(id=uuid4().hex, name="重启凭据验收", endpoint=mock + "/tools/search", enabled=True)
    command(backend, "mutate_search_settings", {"operation": "upsert_profile", "expected_revision": search["revision"], "model_revision": search["model_revision"], "profile": profile, "key_updates": {profile["id"]: {"action": "replace", "value": search_key}}})
    database = work / "data/quillweave.sqlite3"
    master = Path(str(database) + ".key")
    original_master = master.read_bytes()
    if len(original_master) != 32 or not saved["key_status"][model_id]:
        raise AssertionError("The credential master key or initial save is incomplete")
    for suffix in ["", "-wal", "-shm"]:
        file = Path(str(database) + suffix)
        if file.exists() and any(secret.encode() in file.read_bytes() for secret in [model_key, search_key]):
            raise AssertionError("A plaintext credential reached the database or its journal")
    stop_process(services[0])
    subprocess.run(["go", "build", "-o", str(binary), "./cmd/preview"], cwd=ROOT, check=True)
    services[0] = subprocess.Popen([str(binary)], cwd=ROOT, env=environment, stdout=log, stderr=log, start_new_session=True)
    wait_ready(backend, services[0])
    restored = command(backend, "get_model_settings", {})
    restored_search = command(backend, "get_search_settings", {})
    if not restored["key_status"][model_id] or not restored_search["key_status"][profile["id"]]:
        raise AssertionError("Recompilation and process restart lost a saved model or search credential")
    if master.read_bytes() != original_master:
        raise AssertionError("Recompilation replaced the installation master key")
    if any(secret in json.dumps([restored, restored_search]) for secret in [model_key, search_key]):
        raise AssertionError("A restarted settings response exposed plaintext credentials")
    source = """async page => {
      await page.reload();
      await page.locator('.sidebar-bottom').getByRole('button', {name:'设置',exact:true}).click();
      const field = page.getByLabel('API 密钥', {exact:true});
      await page.waitForFunction(() => [...document.querySelectorAll('input')].some(input => input.getAttribute('aria-label') === 'API 密钥' && input.value === '' && input.placeholder === '••••••••'));
      if (await field.getAttribute('type') !== 'password') throw new Error('The restored key is visible');
      await page.getByText('API 密钥加密保存在本地数据库中，重启后自动恢复，本地模型可以留空。', {exact:true}).waitFor();
      await page.screenshot({path:__RESTART_SCREENSHOT__,fullPage:true});
      return {restart:true,modelKeyConfigured:true,searchKeyConfigured:true,plaintextExposed:false};
    }""".replace("__RESTART_SCREENSHOT__", json.dumps(str(work / "model-settings-restarted.png")))
    result = subprocess.run(cli + ["run-code", source], cwd=work, capture_output=True, text=True, timeout=45)
    if result.returncode or "### Error" in result.stdout:
        raise AssertionError(result.stderr or result.stdout[-3000:])
    print("Credential restart acceptance passed: rebuilt process, model/search keys restored, original master key retained, masked UI and encrypted database verified.", flush=True)


def run(review=False, search_only=False, chat_only=False, editor_only=False):
    """Build, test and clean up isolated services, browser state and all generated artifacts on every path."""
    if not search_only and not chat_only:
        subprocess.run(["node", "--test", str(ROOT / "test/frontend/knowledge_state.test.mjs")], cwd=ROOT, check=True)
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
            if chat_only:
                profile["config"]["model_limits"] = {"context_window_tokens": 32768, "max_input_tokens": 24576, "max_output_tokens": 8192}
                second = json.loads(json.dumps(profile))
                second.update(id="chat-second", name="第二个聊天模型")
                second["config"]["model"] = "chat-fixture-second"
                settings["profiles"].append(second)
                layout = json.loads(json.dumps(profile))
                layout.update(id="chat-layout", name="布局参考模型")
                layout["config"].update(provider_id="openai", model="gpt-6.1-sol", reasoning_effort="xhigh", temperature=None)
                settings["profiles"].append(layout)
                skill_root = work / "skills"
                skill = skill_root / "chat-acceptance"
                skill.mkdir(parents=True)
                (skill / "SKILL.md").write_text("---\nname: chat-acceptance\ndescription: Verify explicit chat skill loading\n---\n\nCHAT_SKILL_INSTRUCTIONS. Preserve every author-approved story fact.\n")
                for index in range(18):
                    layout_skill = skill_root / f"layout-skill-{index:02d}"
                    layout_skill.mkdir()
                    description = "Long skill descriptions wrap across several lines while the checkbox and title stay aligned. " * 4
                    (layout_skill / "SKILL.md").write_text(f"---\nname: layout-skill-{index:02d}\ndescription: {description}\n---\n\nInstructions for layout acceptance.\n")
                command(backend, "set_skill_roots", {"roots": [str(skill_root)]})
            command(backend, "save_model_settings", {"settings": settings, "key_updates": {profile["id"]: {"action": "clear"}}})
            subprocess.run(cli + ["open", frontend], cwd=work, check=True, capture_output=True, text=True, timeout=45)
            case_file = "chat_ui_cases.js" if chat_only else "search_ui_cases.js" if search_only else "studio_ui_cases.js"
            source = (ROOT / "test/frontend" / case_file).read_text().replace("__STUDIO_MOCK_URL__", mock).replace("__STUDIO_ARTIFACT_DIR_JSON__", json.dumps(str(work))).replace("__STUDIO_EDITOR_ONLY__", json.dumps(editor_only)).replace("__STUDIO_EDITOR_CASES__", (ROOT / "test/frontend/editor_ui_cases.js").read_text())
            completed = subprocess.run(cli + ["run-code", source], cwd=work, capture_output=True, text=True, timeout=180)
            if completed.returncode or "### Error" in completed.stdout:
                print(completed.stdout[-4000:], flush=True)
                diagnostic = subprocess.run(cli + ["eval", "() => document.body.innerText"], cwd=work, capture_output=True, text=True, timeout=20)
                print(diagnostic.stdout[-5000:], flush=True)
                print("Fixture model requests: " + str(len(fixture.requests)), flush=True)
                for story in command(backend, "list_projects", {}):
                    tasks = command(backend, "get_runs", {"project_id": story["id"]})
                    print("Task states: " + json.dumps([{key: task.get(key) for key in ["id", "action", "status", "error", "calls"]} for task in tasks]), flush=True)
                    for task in tasks:
                        if task.get("status") == "error":
                            detail = command(backend, "get_job", {"job_id": task["id"]})
                            prompt = detail.get("prompt", {})
                            print("Failed prompt budget: " + json.dumps({"id": task["id"], "tool_chars": prompt.get("tool_chars"), "chars": prompt.get("chars"), "groups": prompt.get("history_groups"), "messages": [{"role": item.get("role"), "chars": len(item.get("content", ""))} for item in prompt.get("messages", [])]}), flush=True)
                raise RuntimeError(completed.stderr or completed.stdout[-2500:])
            stories = command(backend, "list_projects", {})
            if chat_only:
                section = completed.stdout.split("### Result\n", 1)[-1].split("### Ran Playwright code", 1)[0].strip()
                result = json.loads(section)
                if result.get("result") != "PASS: chat workspace" or len(result.get("checks", [])) < 10:
                    raise AssertionError("Chat UI cases did not complete the acceptance matrix")
                requests = [request["body"] for request in fixture.requests if "messages" in request["body"]]
                if not any(item.get("model") == "chat-fixture-second" and "CHAT_SKILL_INSTRUCTIONS" in json.dumps(item) for item in requests):
                    raise AssertionError("The selected model and actual skill instructions did not reach the local provider")
                print(json.dumps(result, ensure_ascii=False), flush=True)
                if review:
                    print("VISUAL_REVIEW_DIRECTORY " + temporary, flush=True)
                    input("Press Enter after visual review; cleanup runs immediately afterwards.\n")
                return
            if search_only:
                section = completed.stdout.split("### Result\n", 1)[-1].split("### Ran Playwright code", 1)[0].strip()
                result = json.loads(section)
                if result.get("result") != "PASS: search settings and research" or len(result.get("checks", [])) < 8:
                    raise AssertionError("Search UI cases did not complete their final oracle")
                print(json.dumps(result, ensure_ascii=False), flush=True)
                return
            if editor_only:
                section = completed.stdout.split("### Result\n", 1)[-1].split("### Ran Playwright code", 1)[0].strip()
                result = json.loads(section)
                if result.get("result") != "PASS: chapter draft, conflict, save ordering and deletion recovery" or len(stories) != 1:
                    raise AssertionError("Editor UI cases did not finish or clean their temporary work")
                if any("messages" in request["body"] for request in fixture.requests):
                    raise AssertionError("Editor-only acceptance dispatched a model request")
                print(json.dumps(result, ensure_ascii=False), flush=True)
                if review:
                    print("VISUAL_REVIEW_DIRECTORY " + temporary, flush=True)
                    input("Press Enter after visual review; cleanup runs immediately afterwards.\n")
                return
            if len(stories) != 2:
                raise AssertionError("UI flow did not preserve the original and restored stories")
            project = command(backend, "get_project", {"project_id": stories[0]["id"]})
            if project["studio"]["target_chapters"] != 500 or not project["chapters"] or not project["sources"] or not project["studio"]["memories"]:
                raise AssertionError("UI actions did not persist the complete story workflow")
            model_requests = [request for request in fixture.requests if "messages" in request["body"]]
            if not model_requests or "简体中文" not in model_requests[0]["body"]["messages"][0]["content"]:
                raise AssertionError("Chinese interface language did not reach the real provider adapter")
            verify_credential_restart(backend, mock, binary, environment, services, log, cli, work)
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
    parser.add_argument("--chat-only", action="store_true", help="Exercise model selection, commands, streaming, skills and session recovery")
    parser.add_argument("--editor-only", action="store_true", help="Exercise manuscript drafts, conflict recovery and chapter deletion without model execution")
    options = parser.parse_args()
    if sum([options.search_only, options.chat_only, options.editor_only]) > 1:
        parser.error("Choose only one focused UI suite")
    run(options.review, options.search_only, options.chat_only, options.editor_only)
