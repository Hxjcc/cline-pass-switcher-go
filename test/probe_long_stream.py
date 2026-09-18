import http.client
import json
import os
import time

admin_key = os.environ.get("CLINE_PROXY_KEY") or os.environ.get("X_ADMIN_KEY")
if not admin_key:
    raise SystemExit("set CLINE_PROXY_KEY (or X_ADMIN_KEY) before running this probe")

body = json.dumps({
    "model": "cline-pass/deepseek-v4.1-flash",
    "input": "Solve this carefully, thinking for as long as you need: For every integer n from 1 to 12, determine the exact number of ways to write n as a sum of distinct odd parts, and prove your formula by induction. Show all reasoning.",
    "stream": True,
    "reasoning": {"effort": "high"},
}).encode()

conn = http.client.HTTPConnection("127.0.0.1", 3123, timeout=600)
conn.request("POST", "/v1/responses", body=body, headers={
    "Content-Type": "application/json",
    "X-Admin-Key": admin_key,
})
start = time.time()
resp = conn.getresponse()
print("status", resp.status, "proto", resp.version, flush=True)
print("ctype", resp.getheader("Content-Type"), flush=True)
last = start
events = 0
max_gap = 0.0
total_bytes = 0
try:
    while True:
        chunk = resp.read(4096)
        if not chunk:
            break
        now = time.time()
        gap = now - last
        max_gap = max(max_gap, gap)
        last = now
        total_bytes += len(chunk)
        events += chunk.count(b"event:")
        if events <= 3 or gap > 5:
            print(f"t={now-start:7.2f}s gap={gap:6.2f}s bytes={len(chunk)} events={events}", flush=True)
except Exception as exc:
    print(f"EXCEPTION at t={time.time()-start:.2f}s: {type(exc).__name__}: {exc}", flush=True)
print(f"=== done events={events} bytes={total_bytes} elapsed={time.time()-start:.2f}s max_gap={max_gap:.2f}s ===", flush=True)
