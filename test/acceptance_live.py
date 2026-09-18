"""Opt-in real-provider acceptance in a disposable fixture directory.

Reads the proxy key locally; never prints it or writes it to the report. The
Codex subprocess ignores user configuration and is limited to workspace-write.
"""
import argparse
import http.client
import http.server
import json
import os
from pathlib import Path
import struct
import subprocess
import threading
import time
import urllib.request
import urllib.parse
import zlib


def png_fixture(path):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    raw = (b"\0" + bytes((230, 30, 30)) * 64) * 64
    path.write_bytes(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", 64, 64, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))


def request_json(base, key, endpoint, body):
    request = urllib.request.Request(base + endpoint, data=json.dumps(body).encode(), headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    with urllib.request.urlopen(request, timeout=180) as response:
        return json.load(response)


def text_output(response):
    return "\n".join(part.get("text", "") for item in response.get("output", []) if item.get("type") == "message" for part in item.get("content", []) if part.get("type") == "output_text")


class AcceptanceRelay(http.server.BaseHTTPRequestHandler):
    """Record only protocol shapes/tool names; forward bodies without logging them."""
    def log_message(self, *args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        payload = json.loads(body)
        calls, images = [], 0
        for item in payload.get("input", []) if isinstance(payload.get("input"), list) else []:
            if not isinstance(item, dict):
                continue
            if item.get("type") in ("function_call", "custom_tool_call"):
                calls.append(item.get("name", ""))
            for field in ("content", "output"):
                parts = item.get(field, [])
                if isinstance(parts, list):
                    images += sum(isinstance(part, dict) and part.get("type") in ("input_image", "image_url", "image") for part in parts)
        self.server.observations.append({"path": self.path, "calls": calls, "image_parts": images})
        self.forward("POST", body)

    def do_GET(self):
        self.forward("GET", None)

    def forward(self, method, body):
        target = self.server.target
        connection_class = http.client.HTTPSConnection if target.scheme == "https" else http.client.HTTPConnection
        connection = connection_class(target.hostname, target.port, timeout=180)
        try:
            headers = {"Content-Type": "application/json", "Authorization": self.headers.get("Authorization", "")}
            connection.request(method, self.path, body=body, headers=headers)
            response = connection.getresponse()
            self.send_response(response.status)
            self.send_header("Content-Type", response.getheader("Content-Type", "application/json"))
            self.end_headers()
            while data := response.read1(32768):
                self.wfile.write(data)
                self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            pass
        finally:
            connection.close()


def inspect_cli_evidence(stdout, observations, result, returncode):
    events = [json.loads(line) for line in stdout.splitlines() if line.strip().startswith("{")]
    completed_items = [event.get("item", {}) for event in events if event.get("type") == "item.completed"]
    seen_calls = sorted({call for observation in observations for call in observation["calls"]})
    image_roundtrip = any(observation["image_parts"] > 0 for observation in observations)
    remote_compactions = sum(observation["path"].endswith("/compact") for observation in observations)
    # The CLI emits this warning after its client-side compaction completes.
    local_compaction_notices = sum("multiple compactions" in item.get("message", "") for item in completed_items)
    file_change_ok = any(item.get("type") == "file_change" and item.get("status") == "completed"
                         and any(Path(change.get("path", "")).name == "result.txt" for change in item.get("changes", [])) for item in completed_items)
    readback_ok = any(item.get("type") == "command_execution" and item.get("status") == "completed"
                      and "result.txt" in item.get("command", "") and "COBALT-742" in item.get("aggregated_output", "")
                      and "red" in item.get("aggregated_output", "").lower() for item in completed_items)
    final_ok = any(item.get("type") == "agent_message" and "ACCEPTANCE_OK" in item.get("text", "") for item in completed_items)
    cli_ok = (returncode == 0 and "COBALT-742" in result and "red" in result.lower() and final_ok
              and any(call.endswith("view_image") for call in seen_calls) and image_roundtrip and file_change_ok and readback_ok)
    return {"cli_ok": cli_ok, "seen_calls": seen_calls, "image_roundtrip": image_roundtrip,
            "file_change_ok": file_change_ok, "readback_ok": readback_ok,
            "cli_remote_compactions": remote_compactions, "cli_local_compaction_notices": local_compaction_notices}


def inspect_replay(response):
    text = text_output(response)
    try:
        recalled = json.loads(text)
    except json.JSONDecodeError:
        recalled = None
    schema_ok = (isinstance(recalled, dict) and set(recalled) == {"token", "color", "pending_task"}
                 and all(isinstance(value, str) for value in recalled.values()))
    recall_ok = response.get("status") == "completed" and "COBALT-742" in text and "red" in text.lower() and "DONE" in text
    strict_ok = (schema_ok and recall_ok and recalled["token"] == "COBALT-742"
                 and recalled["color"].lower() == "red" and "DONE" in recalled["pending_task"])
    return {"replay_ok": strict_ok, "replay_state_recall_ok": recall_ok,
            "replay_schema_ok": schema_ok, "replay_status": response.get("status"), "replay_text": text}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--codex", required=True)
    parser.add_argument("--workdir", required=True)
    parser.add_argument("--base-url", default="http://127.0.0.1:3124/v1")
    parser.add_argument("--model", default="cline-pass/glm-5.3-flash")
    parser.add_argument("--auto-compact-limit", type=int)
    parser.add_argument("--reuse-cli-evidence", action="store_true", help="Re-evaluate saved CLI logs and run only the HTTP compact/replay phase")
    args = parser.parse_args()
    cfg = json.loads(Path(args.config).read_text(encoding="utf-8-sig"))
    key = cfg.get("proxyKey", "")
    work = Path(args.workdir).resolve()
    work.mkdir(parents=True, exist_ok=True)
    if not args.reuse_cli_evidence and (work / "result.txt").exists():
        parser.error("use a fresh workdir so a previous result cannot pass this acceptance run")
    fixture = work / "fixture.txt"
    fixture.write_text("Acceptance token: COBALT-742\nKeep this token unchanged.\n", encoding="utf-8")
    png_fixture(work / "sample.png")
    prompt = ("This is a bounded proxy acceptance test. Work only in this directory. "
              "Use your file/shell tool to read fixture.txt; use view_image to inspect sample.png. "
              "Use apply_patch to create result.txt containing the exact acceptance token read from the fixture "
              "and the dominant image color in English. Read result.txt back with a tool to verify it. "
              "Do not access credentials, environment variables, outside paths, network, or other projects. "
              "Do not ask questions. Finish with ACCEPTANCE_OK, the token and the color.")
    relay = http.server.ThreadingHTTPServer(("127.0.0.1", 0), AcceptanceRelay)
    relay.target = urllib.parse.urlparse(args.base_url)
    relay.observations = []
    threading.Thread(target=relay.serve_forever, daemon=True).start()
    relay_url = "http://127.0.0.1:" + str(relay.server_port) + relay.target.path
    provider = '{name="Review proxy",base_url=' + json.dumps(relay_url) + ',wire_api="responses",env_key="CLINE_REVIEW_PROXY_KEY",requires_openai_auth=false,request_max_retries=0,stream_max_retries=0}'
    command = [args.codex, "exec", "--ignore-user-config", "--ephemeral", "--skip-git-repo-check", "--sandbox", "workspace-write", "--cd", str(work), "--json", "-m", args.model]
    for option in ['model_provider="review_proxy"', 'model_providers.review_proxy=' + provider, 'approval_policy="never"', 'web_search="disabled"', 'model_reasoning_effort="low"', 'windows.sandbox="unelevated"', 'features.multi_agent=false']:
        command += ["-c", option]
    if args.auto_compact_limit:
        command += ["-c", "model_auto_compact_token_limit=" + str(args.auto_compact_limit)]
    command += [prompt]
    env = os.environ.copy()
    env["CLINE_REVIEW_PROXY_KEY"] = key or "local-test"
    start = time.monotonic()
    previous_report = {}
    timed_out = False
    try:
        if args.reuse_cli_evidence:
            previous_report = json.loads((work / "acceptance.json").read_text(encoding="utf-8"))
            completed = subprocess.CompletedProcess(command, previous_report["cli_exit"],
                (work / "cli.jsonl").read_text(encoding="utf-8"), (work / "cli.stderr.log").read_text(encoding="utf-8"))
            relay.observations = json.loads((work / "protocol-shapes.json").read_text(encoding="utf-8"))
        else:
            completed = subprocess.run(command, env=env, cwd=work, capture_output=True, text=True, encoding="utf-8", errors="replace", timeout=240)
    except subprocess.TimeoutExpired as error:
        timed_out = True
        def decode(value):
            return value.decode("utf-8", errors="replace") if isinstance(value, bytes) else (value or "")
        completed = subprocess.CompletedProcess(command, -1, decode(error.stdout), decode(error.stderr))
    finally:
        relay.shutdown()
        relay.server_close()
    # Reports only describe the deliberately public test fixture, with any
    # accidental matching secret scrubbed before local persistence/output.
    stdout, stderr = completed.stdout, completed.stderr
    if key:
        stdout, stderr = stdout.replace(key, "[redacted]"), stderr.replace(key, "[redacted]")
    (work / "cli.jsonl").write_text(stdout, encoding="utf-8")
    (work / "cli.stderr.log").write_text(stderr, encoding="utf-8")
    result = (work / "result.txt").read_text(encoding="utf-8") if (work / "result.txt").exists() else ""
    evidence = inspect_cli_evidence(stdout, relay.observations, result, completed.returncode)
    if args.auto_compact_limit:
        evidence["cli_ok"] = evidence["cli_ok"] and (evidence["cli_remote_compactions"] > 0 or evidence["cli_local_compaction_notices"] > 0)
    report = {"model": args.model, "cli_exit": completed.returncode, "cli_seconds": previous_report.get("cli_seconds", round(time.monotonic()-start, 2)), "fixture_result": result, "reused_cli_evidence": args.reuse_cli_evidence, **evidence}
    if timed_out:
        report["error"] = "CLI exceeded 240-second acceptance budget"
    (work / "protocol-shapes.json").write_text(json.dumps(relay.observations, indent=2), encoding="utf-8")
    (work / "acceptance.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(report), flush=True)
    if not report["cli_ok"]:
        print(stderr[-1500:], flush=True)
        print(stdout[-2000:], flush=True)
        raise SystemExit(1)

    # Exercise real summary generation and continuation against the same proxy.
    # This is an HTTP acceptance step; it does not claim a CLI auto-compaction.
    history = [{"role": "user", "content": "Our exact project token is COBALT-742. Keep it unchanged. The image color is red."},
               {"role": "assistant", "content": "Created result.txt and verified COBALT-742 and red. Pending task: add a DONE line without changing the token."}]
    compact = request_json(args.base_url, key, "/responses/compact", {"model": args.model, "input": history, "reasoning": {"effort": "low"}})
    compact_ok = compact.get("object") == "response.compaction" and any(item.get("type") == "compaction" for item in compact.get("output", []))
    replay = request_json(args.base_url, key, "/responses", {"model": args.model, "input": compact["output"] + [{"role": "user", "content": "Only recall prior state. Do not perform or claim any new action. Return token, color and the pending task as JSON."}], "max_output_tokens": 1024, "reasoning": {"effort": "low"},
        "text": {"format": {"type": "json_schema", "name": "recalled_state", "strict": True, "schema": {"type": "object", "properties": {"token": {"type": "string"}, "color": {"type": "string"}, "pending_task": {"type": "string"}}, "required": ["token", "color", "pending_task"], "additionalProperties": False}}}})
    report.update(compact_ok=compact_ok, **inspect_replay(replay))
    (work / "acceptance.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(report, ensure_ascii=False), flush=True)
    if not compact_ok or not report["replay_ok"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
