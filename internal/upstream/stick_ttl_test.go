package upstream

import (
	"testing"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// The TTL should track the provider cache lifetime, so it is configurable; an
// empty or broken value keeps the built-in default.
func TestStickTTLUsesTheConfiguredValue(t *testing.T) {
	st := newStreamTestStore(t, "http://127.0.0.1:1")
	service := New(st)

	if got := service.stickTTL(); got != defaultSessionStickTTL {
		t.Fatalf("default TTL = %v, want %v", got, defaultSessionStickTTL)
	}
	for _, testCase := range []struct {
		value string
		want  time.Duration
	}{
		{"2h", 2 * time.Hour},
		{"10m", 10 * time.Minute},
		{"", defaultSessionStickTTL},
		{"nonsense", defaultSessionStickTTL},
		{"-5m", defaultSessionStickTTL},
	} {
		if err := st.UpdateConfig(func(cfg *model.Config) { cfg.StickTTL = testCase.value }); err != nil {
			t.Fatal(err)
		}
		if got := service.stickTTL(); got != testCase.want {
			t.Fatalf("stickTTL(%q) = %v, want %v", testCase.value, got, testCase.want)
		}
	}
}

// A conversation whose TTL elapsed is a miss, which is what makes the knob
// observable at runtime.
func TestLookupStickForgetsAnExpiredConversation(t *testing.T) {
	st := newStreamTestStore(t, "http://127.0.0.1:1")
	service := New(st)
	if err := st.UpdateConfig(func(cfg *model.Config) { cfg.StickTTL = "1ms" }); err != nil {
		t.Fatal(err)
	}
	account := st.Config().Accounts[0]
	service.rememberStick("session-a", account, "deepseek")
	time.Sleep(5 * time.Millisecond)
	if accountID, upstream := service.LookupStick("session-a"); accountID != "" || upstream != "" {
		t.Fatalf("an expired stick must be a miss, got %q/%q", accountID, upstream)
	}
}
