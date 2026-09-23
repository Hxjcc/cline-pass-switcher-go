"""Move shared helpers and the streaming endpoint tests out of server_test.go.
Pure relocation: every block is copied verbatim."""
import pathlib, re

path = pathlib.Path("internal/httpapi/server_test.go")
lines = path.read_text(encoding="utf-8").split("\n")
top = re.compile(r"^(func|type|var|const) ")

starts = [i for i, l in enumerate(lines) if top.match(l)]
blocks = []
for idx, start in enumerate(starts):
    begin = start
    while begin - 1 >= 0 and lines[begin - 1].startswith("//"):
        begin -= 1
    end = starts[idx + 1] if idx + 1 < len(starts) else len(lines)
    if idx + 1 < len(starts):
        while end - 1 > start and lines[end - 1].startswith("//"):
            end -= 1
    chunk = lines[begin:end]
    while chunk and chunk[-1].strip() == "":
        chunk.pop()
    first = next(l for l in lines[begin:end] if top.match(l))
    name = re.match(r"(func|type|var|const)\s*(?:\([^)]*\)\s*)?([A-Za-z0-9_]+)", first).group(2)
    blocks.append({"name": name, "text": "\n".join(chunk), "begin": begin, "end": end})

helpers = {"localRequest", "newTestServer", "accountStats"}
streaming = {
    "TestStreamingResponsesEndpointEmitsResponsesEvents",
    "TestStreamingResponsesReplaysBufferedCompletion",
    "TestStreamingChatSynthesizesChunkFromBufferedCompletion",
    "TestResponsesShareKeyCoversSamplingParameters",
    "TestSharedResponsesStreamCoalescesDuplicateClients",
    "TestStreamingResponsesFailsOverToHealthyAccountOn401",
    "TestStreamingResponsesOutlivesIdleWindowWhileDataFlows",
    "TestStreamingResponsesEmitsKeepaliveDuringUpstreamSilence",
    "TestStreamingChatEmitsKeepaliveDuringUpstreamSilence",
    "TestStreamingResponsesPreservesUpstreamErrorDetails",
}
names = {b["name"] for b in blocks}
for group in (helpers, streaming):
    missing = group - names
    if missing:
        raise SystemExit("missing blocks: %s" % missing)

def body_for(group):
    return "\n\n".join(b["text"] for b in blocks if b["name"] in group)

alias_imports = [
    ("crypto/sha256", "sha256"), ("encoding/base64", "base64"), ("encoding/json", "json"),
    ("io", "io"), ("io/fs", "fs"), ("net/http", "http"), ("net/http/httptest", "httptest"),
    ("slices", "slices"), ("strings", "strings"), ("sync", "sync"), ("sync/atomic", "atomic"),
    ("testing", "testing"), ("testing/fstest", "fstest"), ("time", "time"),
    ("github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model", "model"),
    ("github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/responses", "responsesbridge"),
    ("github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store", "store"),
    ("github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream", "upstream"),
    ("github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/webassets", "webassets"),
]

def with_header(body):
    used = [p for p, alias in alias_imports if re.search(r"\b%s\." % alias, body)]
    std = [p for p in used if not p.startswith("github.com")]
    ext = [p for p in used if p.startswith("github.com")]
    rows = ['\t"%s"' % p for p in std]
    if std and ext:
        rows.append("")
    for p in ext:
        if p.endswith("/responses"):
            rows.append('\tresponsesbridge "%s"' % p)
        else:
            rows.append('\t"%s"' % p)
    return "package httpapi\n\nimport (\n" + "\n".join(rows) + "\n)\n\n" + body + "\n"

pathlib.Path("internal/httpapi/helpers_test.go").write_text(with_header(body_for(helpers)), encoding="utf-8")
pathlib.Path("internal/httpapi/stream_endpoint_test.go").write_text(with_header(body_for(streaming)), encoding="utf-8")

moved = helpers | streaming
rest = "\n\n".join(b["text"] for b in blocks if b["name"] not in moved)
path.write_text(with_header(rest), encoding="utf-8")
print("helpers moved:", sorted(helpers))
print("tests moved:", len(streaming))
print("blocks kept in server_test.go:", len(blocks) - len(moved))
