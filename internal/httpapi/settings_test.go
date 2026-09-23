package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

// mustServer builds the HTTP layer around a store the caller already opened,
// for tests that need to seed config.json before startup.
func mustServer(t *testing.T, st *store.Store) *Server {
	t.Helper()
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>"), Mode: 0o644},
	}
	server, err := New(st, upstream.New(st), fs.FS(assets))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func settingsByKey(t *testing.T, server *Server, adminKey string) map[string]map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	getWithAdminKey(server, "/api/settings", adminKey, recorder)
	if recorder.Code != http.StatusOK {
		t.Fatalf("settings endpoint failed: %d %s", recorder.Code, recorder.Body)
	}
	var payload struct {
		Settings []map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]map[string]any, len(payload.Settings))
	for _, row := range payload.Settings {
		key, _ := row["key"].(string)
		byKey[key] = row
	}
	return byKey
}

// The console has to say which layer supplied each value, because that is the
// only way to tell "my .env change took effect" from "an old config.json value
// or a built-in default is still in force".
func TestEffectiveSettingsReportTheirSource(t *testing.T) {
	dir := t.TempDir()
	// A config written before startup: these two keys are explicitly configured.
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{
	  "proxyKey": "master-key",
	  "adminKey": "console-key",
	  "compactionMinOutputTokens": 12288,
	  "shellCompat": "cmd"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	server := mustServer(t, st)

	// One environment override, applied at startup exactly like Compose does.
	t.Setenv("SHELL_COMPAT_ENFORCE", "true")
	if err := st.UpdateConfig(func(cfg *model.Config) { cfg.ShellCompatEnforce = true }); err != nil {
		t.Fatal(err)
	}

	rows := settingsByKey(t, server, "console-key")

	if row := rows["compactionMinOutputTokens"]; row["value"] != "12288" || row["source"] != "config" {
		t.Fatalf("a config.json value must be reported as config: %#v", row)
	}
	if row := rows["shellCompat"]; row["source"] != "config" || row["value"] != "cmd" {
		t.Fatalf("shell compatibility came from the file: %#v", row)
	}
	if row := rows["shellCompatEnforce"]; row["source"] != "env" || row["value"] != "true" {
		t.Fatalf("an environment override must win: %#v", row)
	}
	if row := rows["compactionRecentTokens"]; row["source"] != "default" {
		t.Fatalf("an untouched knob is a built-in default: %#v", row)
	}
	if row := rows["compactionEscalatedCeilingTokens"]; row["source"] != "builtin" {
		t.Fatalf("the retry ceiling is a code constant: %#v", row)
	}
	// Credentials are reported as presence, never as a value.
	if row := rows["proxyKey"]; row["value"] != "已设置" || row["secret"] != true {
		t.Fatalf("the master key row must stay masked: %#v", row)
	}
	if row := rows["adminKey"]; row["value"] != "已设置" || row["source"] != "config" {
		t.Fatalf("the admin key row must stay masked: %#v", row)
	}
}

// The compaction knobs the console shows must match what the request path
// actually uses - a report that disagreed with behaviour would be worse than
// none.
func TestEffectiveSettingsMatchTheRunningConfig(t *testing.T) {
	st, server := newTestServer(t)
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.CompactionRecentTokens = 4096
		cfg.CompactionReasoningEffort = "high"
		cfg.CompactionMinOutputTokens = 8192
	}); err != nil {
		t.Fatal(err)
	}
	rows := settingsByKey(t, server, "")
	want := map[string]string{
		"compactionRecentTokens":    "4096",
		"compactionReasoningEffort": "high",
		"compactionMinOutputTokens": "8192",
	}
	for key, value := range want {
		if row := rows[key]; row["value"] != value {
			t.Fatalf("%s reported %#v, want %q", key, row["value"], value)
		}
	}
}
