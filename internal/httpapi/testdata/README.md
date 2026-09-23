# Captured upstream streams

`upstream-stream-deepseek-v4-1-flash.json` is one real Cline Pass SSE capture.
It exists so the streaming adapter is tested against bytes a gateway actually
sent, instead of against a stream somebody imagined.

## Refreshing it

1. **Capture** — POST `/chat/completions` with `"stream": true` to the gateway,
   read the response in 4096-byte pieces, and keep both the body and the length
   of every piece.
2. **Sanitize with a JSON-aware scrubber, never a regex over the raw text** —
   the payloads contain escaped quotes, and a regex rewrite silently produces
   invalid JSON (which is how this file was first built, and the test caught
   it). Replace stream ids, session ids, provider request/response ids,
   fingerprints and timestamps with fixed placeholders, and replace the model's
   reasoning text with `think`. Keep one multi-byte token (the fixture uses
   `想`) so a one-byte-at-a-time replay has to split a character.
3. **Re-serialize** every `data:` payload as compact JSON and split the body at
   4096 bytes again.
4. Do not hand-edit the file. The test checks `bodyBytes` against the body and
   requires the trailing `data: [DONE]`.

## What consumes it

`internal/httpapi/stream_fixture_test.go` replays the body at four chunk sizes
(the captured split, one write, one byte per write, seven bytes per write) and
asserts the emitted Responses event sequence, the assembled text and the usage
are identical across all of them. A second test corrupts one event and expects
the turn to fail with a recorded reason.
