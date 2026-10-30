package responses

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

func TestReasoningEnvelopeAuthenticatesContentAndClient(t *testing.T) {
	codec, err := NewReasoningCodec(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	a, b := codec.Scoped("issued:a"), codec.Scoped("issued:b")
	first, second := a.Encode("private reasoning"), a.Encode("private reasoning")
	if first == second || strings.Contains(first, "private reasoning") {
		t.Fatal("envelope is not randomized encrypted content")
	}
	if got, err := a.Decode(first); err != nil || got != "private reasoning" {
		t.Fatal("reasoning round trip failed", err)
	}
	if _, err := b.Decode(first); err == nil {
		t.Fatal("another client scope could decrypt the envelope")
	}
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(first, reasoningEnvelopePrefix))
	raw[len(raw)-1] ^= 1
	if _, err := a.Decode(reasoningEnvelopePrefix + base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered reasoning was accepted")
	}
}

func TestEncryptedReasoningIncludeAndStatelessReplay(t *testing.T) {
	codec, _ := NewReasoningCodec(bytes.Repeat([]byte{8}, 32))
	options := Options{ReasoningCodec: codec.Scoped("master"), ReplayReasoning: true}
	_, ctx, err := ToChatWithOptions(map[string]any{"model": "test", "input": "hello", "include": []any{"reasoning.encrypted_content"}}, options)
	if err != nil {
		t.Fatal(err)
	}
	chat := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "OK", "reasoning_content": "preserve this reasoning"}, "finish_reason": "stop"}}}
	response, err := FromChat(chat, ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := jsonx.Slice(response["output"])
	found := false
	for _, raw := range input {
		item := jsonx.Map(raw)
		if item["type"] == "reasoning" {
			found = true
			if !strings.HasPrefix(jsonx.String(item["encrypted_content"]), reasoningEnvelopePrefix) {
				t.Fatal("requested encrypted reasoning missing")
			}
			delete(item, "summary")
			delete(item, "content")
		}
	}
	if !found {
		t.Fatal("fixture produced no reasoning")
	}
	body := map[string]any{"model": "test", "input": append(input, map[string]any{"role": "user", "content": "continue"})}
	before, _ := json.Marshal(body)
	replay, _, err := ToChatWithOptions(body, options)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(body)
	if !bytes.Equal(before, after) {
		t.Fatal("replay mutated the original request")
	}
	preserved := false
	for _, raw := range jsonx.Slice(replay["messages"]) {
		if jsonx.Map(raw)["reasoning_content"] == "preserve this reasoning" {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("encrypted-only reasoning was not restored")
	}
}
