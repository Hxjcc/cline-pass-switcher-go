package model

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicOriginRejectsNonOrigins(t *testing.T) {
	for _, v := range []string{"example.com", "//example.com", "ftp://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com//", "https://example.com?", "https://example.com?x=1", "https://example.com#", "https://example.com/#fragment", "https://example.com:99999", "https://example.com:"} {
		if _, err := PublicOrigin(v); err == nil {
			t.Errorf("accepted %q", v)
		}
	}
	for _, v := range []string{"http://localhost:3123", "https://example.com/", "http://[::1]:3123"} {
		if _, err := PublicOrigin(v); err != nil {
			t.Error(v, err)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"publicBaseUrl":"https://example.com//"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("normalization silently accepted a path")
	}
}

func TestDataDirectoryIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDataDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode: %v %v", info, err)
	}
}

func TestAdminClientCollisionIsRejectedBeforeNormalization(t *testing.T) {
	for _, environment := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "environment"}[environment], func(t *testing.T) {
			t.Setenv("ADMIN_KEY", "")
			t.Setenv("PROXY_KEY", "")
			t.Setenv("PUBLIC_BASE_URL", "")
			admin := "client-key"
			if environment {
				admin = ""
				t.Setenv("ADMIN_KEY", "client-key")
			}
			path := filepath.Join(t.TempDir(), "config.json")
			raw := `{"adminKey":"` + admin + `","proxyKeys":[{"id":"issued","key":"client-key","enabled":true}]}`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("normalization hid an admin/client collision")
			}
		})
	}
}

func TestHistoryBoundsPreserveAccounting(t *testing.T) {
	long := strings.Repeat("界", 3000)
	cost := 0.5
	entry := BoundedHistory(HistoryEntry{Model: long, Session: long, Canonical: long, KeyID: long, AccountID: long, Error: &long, Trace: []Trace{{Upstream: long, Note: long}}, Usage: &UsageStats{Cost: &cost, PromptTokens: 123}, Tools: &ToolSummary{Names: map[string]int{long: 2}, Calls: 2}})
	if len([]rune(entry.Model)) > 256 || len([]rune(entry.Session)) > 256 || len([]rune(*entry.Error)) > 2000 || len([]rune(entry.Trace[0].Note)) > 2000 {
		t.Fatal("unbounded history")
	}
	if *entry.Usage.Cost != 0.5 || entry.Usage.PromptTokens != 123 || entry.Tools.Calls != 2 {
		t.Fatal("accounting changed")
	}
	if entry.KeyID != long || entry.AccountID != long {
		t.Fatal("accounting identities were rewritten")
	}
	if ValidateConfig(Config{ProxyKeys: []ProxyKeyGrant{{ID: long}}}) == nil || ValidateConfig(Config{Accounts: []Account{{ID: long}}}) == nil {
		t.Fatal("oversized accounting identity accepted in configuration")
	}
}
