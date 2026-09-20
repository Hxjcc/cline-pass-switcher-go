package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// Every other test in this repository calls the packages directly. This one
// builds the real binary and drives it over HTTP, which is the only way to
// cover the startup path: environment overrides, opening a data directory from
// scratch, the embedded console, and the graceful shutdown that checkpoints
// the store.
const (
	testAccountKey = "sk-account-secret"
	testProxyKey   = "smoke-proxy-key"
	testModelID    = "cline-pass/test"
	// A port that must lose to the environment override below.
	stalePortInConfig = 3199
)

// syncBuffer collects the child process output. The race detector runs this
// suite, and the pipe copier writes while the test reads on failure.
type syncBuffer struct {
	mu      sync.Mutex
	builder strings.Builder
}

func (buffer *syncBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.builder.Write(data)
}

func (buffer *syncBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.builder.String()
}

type httpResult struct {
	Status int
	Header http.Header
	Body   string
}

// tryRequest keeps the connection error instead of failing: while the server is
// still starting, a refused connection is the expected answer.
func tryRequest(method, url, body, adminKey string) (httpResult, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return httpResult{}, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if adminKey != "" {
		req.Header.Set("Authorization", "Bearer "+adminKey)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{Status: response.StatusCode, Header: response.Header, Body: string(raw)}, nil
}

func request(t *testing.T, method, url, body, adminKey string) httpResult {
	t.Helper()
	result, err := tryRequest(method, url, body, adminKey)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return result
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// newFakeUpstream answers the one endpoint the proxy calls, and refuses to be
// reached with anything but the configured account key.
func newFakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/chat/completions" {
			http.NotFound(writer, req)
			return
		}
		if got := req.Header.Get("Authorization"); got != "Bearer "+testAccountKey {
			t.Errorf("upstream saw %q, want the account key", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"chatcmpl-smoke","object":"chat.completion","model":"`+testModelID+`",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// environmentWith drops any inherited value for the names it sets: a duplicate
// entry would leave the original in front, and that is the one Getenv returns.
func environmentWith(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, replaced := overrides[strings.ToUpper(name)]; replaced {
			continue
		}
		environment = append(environment, entry)
	}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func TestBinaryServesTheFullStack(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the binary")
	}
	name := "cline-pass-switcher-test"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the binary: %v\n%s", err, output)
	}

	upstream := newFakeUpstream(t)
	dataDir := t.TempDir()
	config := map[string]any{
		"port":          stalePortInConfig,
		"proxyKey":      testProxyKey,
		"upstreamBase":  upstream.URL,
		"accounts":      []map[string]any{{"name": "main", "key": testAccountKey, "enabled": true}},
		"accountMode":   "single",
		"activeAccount": 0,
		"knownModels":   []string{testModelID},
		"perModel":      map[string]any{},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	command := exec.Command(binary)
	command.Env = environmentWith(map[string]string{
		"DATA_DIR":                 dataDir,
		"PORT":                     strconv.Itoa(port),
		"BIND_HOST":                "127.0.0.1",
		"PROXY_KEY":                "",
		"CLINE_PASS_KEY":           "",
		"TRUSTED_PROXIES":          "",
		"TRUST_LOCAL_PORT_FORWARD": "",
		"PUBLIC_BASE_URL":          "",
	})
	logs := &syncBuffer{}
	command.Stdout, command.Stderr = logs, logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		health, err := tryRequest(http.MethodGet, base+"/healthz", "", "")
		if err == nil && health.Status == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never became healthy\n%s", logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The environment port wins over the one in config.json.
	if strings.Contains(logs.String(), ":"+strconv.Itoa(stalePortInConfig)) {
		t.Fatalf("PORT should have overridden the config port\n%s", logs.String())
	}

	// The console is embedded: the response has to be the real page.
	index := request(t, http.MethodGet, base+"/", "", "")
	if index.Status != http.StatusOK || !strings.Contains(strings.ToLower(index.Body), "<!doctype") {
		t.Fatalf("console index: %d %.80s", index.Status, index.Body)
	}
	if index.Header.Get("Content-Security-Policy") == "" || index.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("console headers: %v", index.Header)
	}

	// /api/meta stays public; the rest of the API does not.
	meta := request(t, http.MethodGet, base+"/api/meta", "", "")
	if meta.Status != http.StatusOK || !strings.Contains(meta.Body, `"authRequired":true`) || !strings.Contains(meta.Body, `"configured":true`) {
		t.Fatalf("meta: %d %s", meta.Status, meta.Body)
	}
	if anonymous := request(t, http.MethodGet, base+"/api/accounts", "", ""); anonymous.Status != http.StatusUnauthorized {
		t.Fatalf("accounts without a key: %d", anonymous.Status)
	}

	// Even authenticated, the stored key never comes back in the clear.
	accounts := request(t, http.MethodGet, base+"/api/accounts", "", testProxyKey)
	if accounts.Status != http.StatusOK {
		t.Fatalf("accounts with a key: %d %s", accounts.Status, accounts.Body)
	}
	if strings.Contains(accounts.Body, testAccountKey) {
		t.Fatalf("accounts must not expose the stored key: %s", accounts.Body)
	}

	// A completion travels the whole way: proxy key -> account pool -> upstream.
	chat := request(t, http.MethodPost, base+"/v1/chat/completions",
		`{"model":"`+testModelID+`","messages":[{"role":"user","content":"ping"}]}`, testProxyKey)
	if chat.Status != http.StatusOK || !strings.Contains(chat.Body, "pong") {
		t.Fatalf("chat completion: %d %s", chat.Status, chat.Body)
	}

	// …and it is recorded in the request log the console reads.
	history := request(t, http.MethodGet, base+"/api/history?limit=5", "", testProxyKey)
	if history.Status != http.StatusOK {
		t.Fatalf("history: %d %s", history.Status, history.Body)
	}
	var recorded struct {
		Total   int `json:"total"`
		History []struct {
			Model string `json:"model"`
			Usage *struct {
				TotalTokens int `json:"totalTokens"`
			} `json:"usage"`
		} `json:"history"`
	}
	if err := json.Unmarshal([]byte(history.Body), &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Total != 1 || len(recorded.History) != 1 || recorded.History[0].Model != testModelID {
		t.Fatalf("expected exactly one recorded request: %s", history.Body)
	}
	if usage := recorded.History[0].Usage; usage == nil || usage.TotalTokens != 4 {
		t.Fatalf("usage should come from the upstream: %s", history.Body)
	}

	// An interrupt asks for a graceful exit, which checkpoints the store.
	// Windows cannot deliver it through os.Process, so there the run is killed
	// and only the exit itself is checked.
	graceful := command.Process.Signal(os.Interrupt) == nil
	if !graceful {
		_ = command.Process.Kill()
	}
	stopped := make(chan error, 1)
	go func() { stopped <- command.Wait() }()
	select {
	case err := <-stopped:
		finished = true
		if graceful && err != nil {
			t.Fatalf("an interrupt should exit cleanly: %v\n%s", err, logs.String())
		}
	case <-time.After(20 * time.Second):
		_ = command.Process.Kill()
		<-stopped
		finished = true
		t.Fatalf("the process did not exit after an interrupt\n%s", logs.String())
	}
	if !graceful {
		return
	}
	// Close materializes the snapshot, so its presence proves the shutdown path
	// ran instead of the process being torn down.
	if _, err := os.Stat(filepath.Join(dataDir, "metadata.json")); err != nil {
		t.Fatalf("a graceful shutdown should checkpoint the store: %v", err)
	}
}

func captureLog(t *testing.T, run func()) string {
	t.Helper()
	buffer := &syncBuffer{}
	previous, previousFlags := log.Writer(), log.Flags()
	log.SetOutput(buffer)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previous)
		log.SetFlags(previousFlags)
	})
	run()
	return buffer.String()
}

// The warning is the only thing standing between a mistaken port mapping and
// silent administrative exposure, so pin down exactly when it fires.
func TestUnauthenticatedTrustWarnings(t *testing.T) {
	if output := captureLog(t, func() { warnUnauthenticatedAccess(model.Config{}) }); output != "" {
		t.Fatalf("a local setup with neither switch should stay quiet: %q", output)
	}

	// A configured proxy key disables the unauthenticated path entirely.
	secured := captureLog(t, func() {
		warnUnauthenticatedAccess(model.Config{
			ProxyKey:              "configured",
			TrustLocalPortForward: true,
			TrustedProxies:        []string{"10.0.0.1"},
		})
	})
	if secured != "" {
		t.Fatalf("a configured key should silence both warnings: %q", secured)
	}

	forwarded := captureLog(t, func() {
		warnUnauthenticatedAccess(model.Config{TrustLocalPortForward: true})
	})
	for _, want := range []string{"TRUST_LOCAL_PORT_FORWARD", "可忽略", "ports"} {
		if !strings.Contains(forwarded, want) {
			t.Fatalf("port-forward warning is missing %q: %q", want, forwarded)
		}
	}

	proxied := captureLog(t, func() {
		warnUnauthenticatedAccess(model.Config{TrustedProxies: []string{"10.0.0.1", "172.16.0.0/12"}})
	})
	if !strings.Contains(proxied, "10.0.0.1,172.16.0.0/12") {
		t.Fatalf("trusted proxies should be listed: %q", proxied)
	}
	if strings.Contains(proxied, "TRUST_LOCAL_PORT_FORWARD") {
		t.Fatalf("the port-forward warning should not fire on its own: %q", proxied)
	}

	both := captureLog(t, func() {
		warnUnauthenticatedAccess(model.Config{TrustLocalPortForward: true, TrustedProxies: []string{"10.0.0.1"}})
	})
	if lines := strings.Count(strings.TrimSpace(both), "\n") + 1; lines != 2 {
		t.Fatalf("both switches should produce two lines, got %d: %q", lines, both)
	}
}
