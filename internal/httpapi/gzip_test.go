package httpapi

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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

// Hold the upstream open until the client receives content over a real HTTP
// connection. Checking only the finished body would miss buffered Chat output
// and a Responses stream cut short by a writer that cannot flush.
func TestClientStreamAliasesFlushWithAndWithoutGzip(t *testing.T) {
	for _, path := range []string{
		"/chat/completions", "/v1/chat/completions", "/api/v1/chat/completions",
		"/responses", "/v1/responses", "/api/v1/responses",
	} {
		for _, encoding := range []string{"identity", "gzip"} {
			t.Run(path+"/"+encoding, func(t *testing.T) {
				u, us := newHeldUpstream(t)
				_, server := newTestServer(t)
				configureDrainKeys(t, server, us.URL)
				gateway := httptest.NewServer(server)
				defer func() {
					gateway.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := server.Shutdown(ctx); err != nil {
						t.Error(err)
					}
				}()
				release := sync.OnceFunc(func() { close(u.proceed) })
				defer release()

				body, terminal := streamChatBody, "data: [DONE]"
				if strings.HasSuffix(path, "/responses") {
					body = `{"model":"cline-pass/test","input":"hello","stream":true}`
					terminal = "event: response.completed"
				}
				request, err := http.NewRequest(http.MethodPost, gateway.URL+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", "Bearer master")
				request.Header.Set("Accept-Encoding", encoding)
				client := gateway.Client()
				client.Timeout = 5 * time.Second
				response, err := client.Do(request)
				if err != nil {
					t.Fatalf("stream did not reach the client while the upstream was open: %v", err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
					t.Fatalf("expected SSE, got status %d and headers %v", response.StatusCode, response.Header)
				}
				if got := response.Header.Get("Content-Encoding"); got != "" {
					t.Fatalf("client streams must bypass console compression, got %q", got)
				}

				reader := bufio.NewReader(response.Body)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Fatalf("first content was not flushed while the upstream was open: %v", err)
					}
					if strings.Contains(line, `"start"`) {
						break
					}
				}
				release()
				tail, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(tail), `" end"`) || !strings.Contains(string(tail), terminal) {
					t.Fatalf("stream lost its remaining content or completion event: %s", tail)
				}
			})
		}
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
