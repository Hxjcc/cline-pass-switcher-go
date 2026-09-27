package httpapi

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The console polls and re-filters /api/history on a remote connection, so the
// JSON APIs are compressed when the client advertises gzip.
func TestAPIResponsesCompressWhenAccepted(t *testing.T) {
	_, server := newTestServer(t)

	request := localRequest(http.MethodGet, "/api/meta", nil)
	request.Header.Set("Accept-Encoding", "gzip, deflate")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected a gzip response, got %q", got)
	}
	if vary := response.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Fatalf("a compressed response must vary on Accept-Encoding: %q", vary)
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatalf("body is not gzip: %v", err)
	}
	defer reader.Close()
	var payload map[string]any
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		t.Fatalf("compressed body is not the JSON payload: %v", err)
	}
	if _, found := payload["authRequired"]; !found {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestAPIResponsesStayPlainWithoutGzip(t *testing.T) {
	_, server := newTestServer(t)

	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodGet, "/api/meta", nil))
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("unexpected encoding for a plain client: %q", got)
	}
	if !strings.Contains(response.Body.String(), "authRequired") {
		t.Fatalf("plain body should be JSON: %s", response.Body.String())
	}
}

// Preflight has no body, so it must not announce an encoding.
func TestPreflightIsNotCompressed(t *testing.T) {
	_, server := newTestServer(t)

	request := localRequest(http.MethodOptions, "/api/meta", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if got := response.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("preflight must stay uncompressed, got %q", got)
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status: %d", response.Code)
	}
	body, _ := io.ReadAll(response.Body)
	if len(body) != 0 {
		t.Fatalf("preflight must have no body, got %q", body)
	}
}

func TestAcceptsGzipHonoursQualityZero(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/meta", nil)
	request.Header.Set("Accept-Encoding", "gzip;q=0, identity")
	if acceptsGzip(request) {
		t.Fatal("gzip;q=0 means the client refuses gzip")
	}
	request.Header.Set("Accept-Encoding", "br, gzip")
	if !acceptsGzip(request) {
		t.Fatal("gzip should be accepted alongside another encoding")
	}
}
