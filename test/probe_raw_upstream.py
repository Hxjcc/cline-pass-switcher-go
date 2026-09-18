import http.client
import json
import os

key = os.environ.get("CLINE_API_KEY")
if not key:
    raise SystemExit("set CLINE_API_KEY before running this probe")

body = json.dumps({
    "model": "cline-pass/deepseek-v4.1-flash",
    "messages": [{"role": "user", "content": "Say hello and reason briefly."}],
    "stream": True,
    "max_tokens": 64,
    "reasoning_effort": "high",
}).encode()

conn = http.client.HTTPSConnection("api.cline.bot", timeout=120)
conn.request("POST", "/api/v1/chat/completions", body=body, headers={
    "Content-Type": "application/json",
    "Authorization": "Bearer " + key,
    "User-Agent": "cline-pass-switcher-go/1.0",
})
resp = conn.getresponse()
print("status", resp.status, resp.getheader("Content-Type"))
raw = resp.read(4000).decode("utf-8", "replace")
print(raw[:3000])
