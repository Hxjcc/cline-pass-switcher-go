package apierr

import (
	"context"
	"net/http"
	"testing"
)

func TestFromBodyPreservesOpenAIErrorDetails(t *testing.T) {
	body := map[string]any{"error": map[string]any{
		"message": "Rate limit reached",
		"type":    "rate_limit_error",
		"code":    "rate_limit_exceeded",
		"param":   "model",
	}}
	details, found := FromBody(body, http.StatusTooManyRequests)
	if !found {
		t.Fatal("expected an error body")
	}
	if details.Status != http.StatusTooManyRequests ||
		details.Type != "rate_limit_error" ||
		details.Code != "rate_limit_exceeded" ||
		details.Param != "model" ||
		details.Message != "Rate limit reached" {
		t.Fatalf("error details were not preserved: %#v", details)
	}
	normalized := Body(details)
	errorObject := asMap(normalized["error"])
	if errorObject["type"] != "rate_limit_error" || errorObject["code"] != "rate_limit_exceeded" {
		t.Fatalf("normalized body lost fields: %#v", normalized)
	}
}

func TestFromBodyInfersContextLengthAndAuthentication(t *testing.T) {
	contextDetails, found := FromBody(map[string]any{"error": map[string]any{
		"message": "This model's maximum context length is 128000 tokens",
	}}, http.StatusBadRequest)
	if !found || contextDetails.Type != "context_length_exceeded" || contextDetails.Status != http.StatusBadRequest {
		t.Fatalf("context length error was not classified: %#v", contextDetails)
	}

	authDetails, found := FromBody(map[string]any{"error": map[string]any{
		"message": "invalid api key",
	}}, http.StatusUnauthorized)
	if !found || authDetails.Type != "authentication_error" || authDetails.Status != http.StatusUnauthorized {
		t.Fatalf("authentication error was not classified: %#v", authDetails)
	}
}

func TestFromBodyHandlesMiniMaxBaseResp(t *testing.T) {
	details, found := FromBody(map[string]any{"base_resp": map[string]any{
		"status_code": 2013,
		"status_msg":  "insufficient balance",
	}}, http.StatusOK)
	if !found || details.Message != "insufficient balance" || StringValue(details.Code) != "2013" {
		t.Fatalf("MiniMax error envelope was not preserved: %#v", details)
	}
}

func TestNetworkClassifiesTimeouts(t *testing.T) {
	details := Network(context.DeadlineExceeded, "network_error")
	if details.Type != "timeout_error" || details.Status != http.StatusGatewayTimeout {
		t.Fatalf("timeout was not classified: %#v", details)
	}
}

func TestStatusInfersCommonMessages(t *testing.T) {
	if got := Status("model not found"); got != http.StatusNotFound {
		t.Fatalf("model not found status: %d", got)
	}
	if got := Status("rate limit exceeded"); got != http.StatusTooManyRequests {
		t.Fatalf("rate limit status: %d", got)
	}
}
