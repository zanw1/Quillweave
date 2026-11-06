"""Share disposable app services and deterministic external boundaries across frontend test runners."""
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[2]


def available_port():
    """Reserve an unused loopback port without reusing a developer's running service."""
    with socket.socket() as connection:
        connection.bind(("127.0.0.1", 0))
        return connection.getsockname()[1]


def wait_ready(address, process):
    """Wait on actual HTTP readiness rather than using a fixed startup sleep."""
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("Preview process exited during startup")
        try:
            with urllib.request.urlopen(address, timeout=1) as response:
                if response.status == 200:
                    return
        except OSError:
            time.sleep(.1)
    raise TimeoutError("Preview did not become ready")


def command(address, name, args):
    """Use the production loopback command envelope for fixture setup and business-state assertions."""
    request = urllib.request.Request(address + "/api/command", json.dumps({"command": name, "args": args}).encode(), headers={"Content-Type": "application/json", "Origin": address, "X-Quillweave-Client": "1"})
    with urllib.request.urlopen(request, timeout=20) as response:
        payload = json.load(response)
    if not payload.get("ok"):
        raise RuntimeError(payload.get("error", "Command failed"))
    return payload["result"]


class FixtureServer(ThreadingHTTPServer):
    """Record only this test's requests and provide explicit fake external services."""
    daemon_threads = True

    def __init__(self):
        super().__init__(("127.0.0.1", 0), FixtureHandler)
        self.requests = []


class FixtureHandler(BaseHTTPRequestHandler):
    """Simulate external HTTP contracts while every story operation runs against real Go code."""

    def log_message(self, *_args):
        """Keep acceptance logs focused on assertions and process failures."""

    def do_GET(self):
        """Return research excerpts whose status is clearly identified as test data."""
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        result = {"results": [{"title": "明代官制资料（验收夹具）", "url": "https://example.org/history", "content": "隔离测试资料摘录，用于验证检索来源与采纳流程，不代表真实史实。"}]}
        self.wfile.write(json.dumps(result, ensure_ascii=False).encode())

    def do_POST(self):
        """Produce cancellable SSE prose and structured proposals with source-grounded memory quotes."""
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or "{}")
        if "/banks/" in self.path:
            self.server.requests.append({"path": self.path, "body": body})
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"success": True, "results": [{"text": "显式选择的测试记忆响应"}]}, ensure_ascii=False).encode())
            return
        messages = body.get("messages", [])
        self.server.requests.append({"path": self.path, "body": body})
        if self.path.endswith(("/tools/search", "/tools/search_pro", "/tools/fetch")) or body.get("tools"):
            if body.get("query") == "需要备用":
                self.send_response(503)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"error":{"message":"deterministic primary outage"}}')
                return
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            source = {"title": "明代官制资料（验收夹具）", "url": "https://example.org/history", "snippet": "隔离测试摘录，不代表真实史实。", "content": "隔离测试资料摘录，用于验证来源采纳。"}
            if self.path.endswith("/tools/fetch"):
                result = {"title": source["title"], "url": body["url"], "content": source["content"], "markdown": source["content"]}
            elif body.get("tools"):
                result = {"choices": [{"message": {"content": "有引用的中文研究回答。", "annotations": [{"type": "url_citation", "url_citation": source}]}}], "usage": {"prompt_tokens": 11, "completion_tokens": 22, "server_tool_use_details": {"web_search_requests": 1}, "cost": .001}}
            else:
                if not body.get("need_content"):
                    del source["content"]
                if self.path.endswith("/search_pro"):
                    source["chunks"] = [{"text": "隔离测试来源段落，不代表真实史实。"}]
                elif body.get("include_content"):
                    source["text"] = "隔离测试网页正文，不代表真实史实。"
                result = {"results": [source], "search_results": [source]}
            self.wfile.write(json.dumps(result, ensure_ascii=False).encode())
            return
        system = messages[0].get("content", "") if messages else ""
        current = messages[-1].get("content", "") if messages else ""
        if "Use one JSON object per step" in system:
            previous = []
            for item in messages:
                if item.get("role") == "assistant":
                    try:
                        previous.append(json.loads(item.get("content", "")).get("tool"))
                    except (ValueError, AttributeError):
                        pass
            if "propose" in previous:
                response = {"tool": "finish", "text": "我们先打磨人物欲望和记忆交易的代价，由你决定哪些设定值得采纳。"}
            elif "recall" in previous:
                response = {"tool": "propose", "proposal": {"kind": "decision", "title": "记忆交易的代价", "content": "每次交易都要放弃一段重要记忆。"}}
            else:
                response = {"tool": "recall", "query": "记忆交易"}
            text = json.dumps(response, ensure_ascii=False)
        elif "从当前章节中整理" in system or "Extract source-grounded updates" in system:
            match = re.search(r"Current chapter \[C:([^\]]+)\]", current)
            source = match.group(1) if match else ""
            text = json.dumps({"text": "整理了一条有原文依据的待审阅事实。", "proposals": [{"kind": "memory", "title": "铜钥匙转移", "content": "林舟把铜钥匙交给岑月。", "memory": {"kind": "fact", "title": "铜钥匙转移", "entity": "林舟", "content": "林舟把铜钥匙交给岑月。", "state": "active", "source_id": source, "quote": "林舟把铜钥匙交给岑月。"}}]}, ensure_ascii=False)
        elif "至少提出五个" in system or "at least five" in system:
            text = "书名候选：北塔借忆人、潮汐不记名、拾梦的第七夜、铜钥匙以北、遗失的刻度。主角名候选：岑月、闻砚、祝遥、陆见川、郁时白。各候选需结合时代、文化与作者偏好审阅。"
        elif "Respect requested chapter counts" in system:
            text = json.dumps({"text": "先讨论分卷，再滚动细化章节。", "proposals": [{"kind": "plan", "title": "第一卷建议", "content": "追查记忆交易的代价。", "plan": {"level": "volume", "title": "第一卷建议", "start_chapter": 1, "end_chapter": 10, "target_characters": 20000, "goal": "查明交易规则"}}]}, ensure_ascii=False)
        else:
            text = "我们继续讨论人物动机与世界规则。" if "请使用简体中文" in system else "Let us explore character motivation and world rules."
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        try:
            for start in range(0, len(text), 35):
                event = {"choices": [{"delta": {"content": text[start:start + 35]}, "finish_reason": None}]}
                self.wfile.write(("data: " + json.dumps(event, ensure_ascii=False) + "\n\n").encode())
                self.wfile.flush()
                time.sleep(.025)
            self.wfile.write(b'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n')
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass


def stop_process(process):
    """Terminate only a test-owned process group and wait for it before deleting temporary files."""
    if process.poll() is None:
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()


