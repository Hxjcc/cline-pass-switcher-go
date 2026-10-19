package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func TestRecordCapsUpstreamErrorText(t *testing.T) {
	st, server := newTestServer(t)
	message := strings.Repeat("x", maxRecordedErrorRunes*4)
	server.record(context.Background(), model.HistoryEntry{
		Model: "cline-pass/test",
		Error: &message,
	})

	history := st.Metadata().History
	if len(history) != 1 || history[0].Error == nil {
		t.Fatalf("record was not stored: %#v", history)
	}
	if got := len([]rune(*history[0].Error)); got != maxRecordedErrorRunes {
		t.Fatalf("stored error length = %d, want %d", got, maxRecordedErrorRunes)
	}
}
