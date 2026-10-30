package responses

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"maps"
	"strings"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

const reasoningEnvelopePrefix = "ocxr1:"

// ReasoningCodec carries proxy-readable reasoning across stateless requests.
// It encodes the Chat provider's returned text, not private OpenAI model state.
type ReasoningCodec struct {
	aead  cipher.AEAD
	scope string
}

func NewReasoningCodec(key []byte) (*ReasoningCodec, error) {
	if len(key) != 32 {
		return nil, errors.New("reasoning envelope key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &ReasoningCodec{aead: aead}, nil
}

func (c *ReasoningCodec) Scoped(scope string) *ReasoningCodec {
	if c == nil {
		return nil
	}
	return &ReasoningCodec{aead: c.aead, scope: scope}
}
func (c *ReasoningCodec) associatedData() []byte { return []byte("cline-reasoning-v1\x00" + c.scope) }
func (c *ReasoningCodec) Encode(text string) string {
	return reasoningEnvelopePrefix + base64.StdEncoding.EncodeToString(c.aead.Seal(nil, nil, []byte(text), c.associatedData()))
}
func (c *ReasoningCodec) Decode(value string) (string, error) {
	encoded, ok := strings.CutPrefix(value, reasoningEnvelopePrefix)
	if !ok {
		return "", errors.New("foreign reasoning envelope")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", errors.New("invalid reasoning envelope")
	}
	plaintext, err := c.aead.Open(nil, nil, raw, c.associatedData())
	if err != nil {
		return "", errors.New("invalid reasoning envelope or client scope")
	}
	return string(plaintext), nil
}

func requestsEncryptedReasoning(body map[string]any) bool {
	for _, value := range jsonx.Slice(body["include"]) {
		if value == "reasoning.encrypted_content" {
			return true
		}
	}
	return false
}

func replayEncryptedReasoning(body map[string]any, codec *ReasoningCodec) (map[string]any, error) {
	var input []any
	for index, raw := range jsonx.Slice(body["input"]) {
		item := jsonx.Map(raw)
		envelope := jsonx.String(item["encrypted_content"])
		if item["type"] != "reasoning" || !strings.HasPrefix(envelope, reasoningEnvelopePrefix) {
			continue
		}
		if codec == nil {
			return nil, unsupported("input.encrypted_content", "reasoning envelopes without a configured local codec")
		}
		text, err := codec.Decode(envelope)
		if err != nil {
			return nil, &RequestError{Code: "invalid_reasoning_envelope", Param: "input.encrypted_content", Message: err.Error()}
		}
		if input == nil {
			input = append([]any(nil), jsonx.Slice(body["input"])...)
		}
		next := maps.Clone(item)
		next["summary"] = []any{map[string]any{"type": "summary_text", "text": text}}
		delete(next, "content")
		input[index] = next
	}
	if input == nil {
		return body, nil
	}
	next := maps.Clone(body)
	next["input"] = input
	return next, nil
}
