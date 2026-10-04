package upstream

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

func TestSessionKeyUsesOnlyTheClientCacheKey(t *testing.T) {
	body := map[string]any{
		"prompt_cache_key": "session-42",
		"user":             "alice",
		"messages": []any{
			map[string]any{"role": "system", "content": "You are Codex, a coding agent."},
		},
	}
	if got := SessionKey("cline-pass/demo", body); got != "cache\ncline-pass/demo\nsession-42" {
		t.Fatalf("cache key = %q", got)
	}
	delete(body, "prompt_cache_key")
	if got := SessionKey("cline-pass/demo", body); got != "" {
		t.Fatalf("user and system text must not stick a conversation, got %q", got)
	}
}

func TestSessionIDFromHeadersPrefersTheSpecificHeader(t *testing.T) {
	header := http.Header{}
	if got := SessionIDFromHeaders(header); got != "" {
		t.Fatalf("a request without session headers must stay empty, got %q", got)
	}
	header.Set("x-client-request-id", "loose-id")
	header.Set("session_id", "pi-session")
	if got := SessionIDFromHeaders(header); got != "pi-session" {
		t.Fatalf("session_id should win over the generic header, got %q", got)
	}
	header.Del("session_id")
	if got := SessionIDFromHeaders(header); got != "loose-id" {
		t.Fatalf("x-client-request-id fallback = %q", got)
	}
}

func TestSessionIDFromHeadersHashesOversizedValues(t *testing.T) {
	header := http.Header{}
	long := strings.Repeat("x", maxSessionHintLen+1)
	header.Set("session_id", long)
	got := SessionIDFromHeaders(header)
	if len(got) != 65 || got == long || !strings.HasPrefix(got, "h") {
		t.Fatalf("an oversized id should be hashed, got %q", got)
	}
	if again := SessionIDFromHeaders(header); again != got {
		t.Fatalf("hashing must be stable: %q vs %q", got, again)
	}
}

func TestSessionKeyForPrefersTheBodyAndFallsBackToHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("session_id", "pi-session")
	ctx := WithSessionHint(context.Background(), SessionIDFromHeaders(header))
	body := map[string]any{"prompt_cache_key": "codex-session"}
	if got := SessionKeyFor(ctx, "cline-pass/demo", body); got != "cache\ncline-pass/demo\ncodex-session" {
		t.Fatalf("the body key should win: %q", got)
	}
	delete(body, "prompt_cache_key")
	if got := SessionKeyFor(ctx, "cline-pass/demo", body); got != "cache\ncline-pass/demo\npi-session" {
		t.Fatalf("header fallback = %q", got)
	}
	if got := SessionKeyFor(context.Background(), "cline-pass/demo", body); got != "" {
		t.Fatalf("without a body key or a hint nothing may stick, got %q", got)
	}
}

func TestPreferAttemptMovesTheStickyChannelFirst(t *testing.T) {
	attempts := []Attempt{{Upstream: "deepseek"}, {Upstream: "glm"}}
	ordered := PreferAttempt(attempts, "glm")
	if ordered[0].Upstream != "glm" || ordered[1].Upstream != "deepseek" {
		t.Fatalf("sticky channel should lead: %#v", ordered)
	}
	if attempts[0].Upstream != "deepseek" {
		t.Fatal("reordering should not rewrite the original slice")
	}
	if got := PreferAttempt(attempts, ""); len(got) != 2 || got[0].Upstream != "deepseek" {
		t.Fatalf("an auto route has nothing to reorder: %#v", got)
	}
}

func TestStickFollowsTheAccountUntilTheKeyChangesOrItExpires(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{{Name: "main", Key: "old-key", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	service := New(st)
	account := st.Config().Accounts[0]
	service.rememberStick("cache\nmodel\nsession", account, "glm")
	id, upstream := service.LookupStick("cache\nmodel\nsession")
	if id != account.ID || upstream != "glm" {
		t.Fatalf("stick = %s %s", id, upstream)
	}

	ctx := WithStick(context.Background(), "cache\nmodel\nsession", account.ID)
	service.observeStick(ctx, account, "glm", http.StatusUnauthorized)
	if id, _ := service.LookupStick("cache\nmodel\nsession"); id != "" {
		t.Fatal("an authentication failure should drop the stick")
	}

	service.rememberStick("cache\nmodel\nsession", account, "")
	saved := service.sticks["cache\nmodel\nsession"]
	saved.until = time.Now().Add(-time.Second)
	service.sticks["cache\nmodel\nsession"] = saved
	if id, _ := service.LookupStick("cache\nmodel\nsession"); id != "" {
		t.Fatal("an expired stick should be a miss")
	}

	service.rememberStick("cache\nmodel\nsession", account, "glm")
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts[0].Key = "new-key"
	}); err != nil {
		t.Fatal(err)
	}
	if id, _ := service.LookupStick("cache\nmodel\nsession"); id != "" {
		t.Fatal("rotating the key should drop the stick")
	}
}

func TestStickyAccountIsSkippedWhenItsQuotaIsFull(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.AccountMode = "single"
		cfg.ActiveAccount = 0
		cfg.Accounts = []model.Account{
			{Name: "full", Key: "key-a", Enabled: true},
			{Name: "spare", Key: "key-b", Enabled: true},
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := New(st)
	accounts := st.Config().Accounts
	reset := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	service.noteQuota(accounts[0], AccountQuota{OK: true, Limits: []QuotaLimit{{Type: "weekly", PercentUsed: 100, ResetsAt: reset}}})
	service.noteQuota(accounts[1], AccountQuota{OK: true, Limits: []QuotaLimit{{Type: "weekly", PercentUsed: 10, ResetsAt: reset}}})

	ctx := WithStick(context.Background(), "cache\nmodel\nsession", accounts[0].ID)
	picked := service.pickAccount(ctx)
	if picked.Name != "spare" {
		t.Fatalf("a full sticky account should yield to the spare, got %#v", picked)
	}
}

// A conversation the user abandoned never comes back, so its entry has to be
// swept: the lookup that deletes it only runs for sessions that return.
func TestStickSweepDropsAbandonedConversations(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpdateConfig(func(cfg *model.Config) {
		cfg.Accounts = []model.Account{{Name: "main", Key: "key-a", Enabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	service := New(st)
	account := st.Config().Accounts[0]
	entry := func(until time.Time) sessionStick {
		return sessionStick{accountID: account.ID, upstream: "glm", keyHash: accountKeyHash(account.Key), until: until}
	}
	service.sticks = map[string]sessionStick{
		"cache\nmodel\nabandoned": entry(time.Now().Add(-time.Minute)),
		"cache\nmodel\nlive":      entry(time.Now().Add(time.Minute)),
	}

	service.rememberStick("cache\nmodel\nfresh", account, "glm")
	if _, found := service.sticks["cache\nmodel\nabandoned"]; found {
		t.Fatal("an expired conversation should be swept away")
	}
	if _, found := service.sticks["cache\nmodel\nlive"]; !found {
		t.Fatal("a live conversation must survive the sweep")
	}
	if _, found := service.sticks["cache\nmodel\nfresh"]; !found {
		t.Fatal("the recorded conversation must be stored")
	}

	// The interval keeps a busy service from walking the map on every response.
	service.stickSweepAt = time.Now()
	service.sticks["cache\nmodel\nlate"] = entry(time.Now().Add(-time.Second))
	service.rememberStick("cache\nmodel\nsecond", account, "glm")
	if _, found := service.sticks["cache\nmodel\nlate"]; !found {
		t.Fatal("the sweep should be throttled between intervals")
	}
}
