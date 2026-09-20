package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

type historyPayload struct {
	History []model.HistoryEntry `json:"history"`
	Total   int                  `json:"total"`
	Offset  int                  `json:"offset"`
	Limit   int                  `json:"limit"`
	HasMore bool                 `json:"hasMore"`
}

func getHistory(t *testing.T, server *Server, query string) (historyPayload, string) {
	t.Helper()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, localRequest(http.MethodGet, "/api/history"+query, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/history%s: %d %s", query, response.Code, response.Body.String())
	}
	var payload historyPayload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload, response.Body.String()
}

// seedHistory writes five records: odd indices succeed, even ones fail. The
// newest entry (TS 1004) therefore has an error.
func seedHistory(t *testing.T, st interface {
	Record(model.HistoryEntry) error
}) {
	t.Helper()
	for index := 0; index < 5; index++ {
		entry := model.HistoryEntry{
			TS:      int64(1000 + index),
			Model:   "cline-pass/alpha",
			Account: "main",
		}
		if index%2 == 0 {
			message := "upstream exploded " + strconv.Itoa(index)
			entry.Error = &message
		}
		if err := st.Record(entry); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHistoryEndpointPagesNewestFirst(t *testing.T) {
	st, server := newTestServer(t)
	seedHistory(t, st)

	first, _ := getHistory(t, server, "?limit=2")
	if first.Total != 5 || first.Limit != 2 || first.Offset != 0 || !first.HasMore {
		t.Fatalf("first page = %#v", first)
	}
	if len(first.History) != 2 || first.History[0].TS != 1004 || first.History[1].TS != 1003 {
		t.Fatalf("first page should start at the newest record: %#v", first.History)
	}

	last, _ := getHistory(t, server, "?limit=2&offset=4")
	if last.HasMore || last.Offset != 4 || len(last.History) != 1 || last.History[0].TS != 1000 {
		t.Fatalf("last page = %#v", last)
	}

	// An offset past the end returns an empty array rather than null.
	empty, body := getHistory(t, server, "?offset=99")
	if empty.History == nil || len(empty.History) != 0 || empty.HasMore {
		t.Fatalf("offset past the end = %s", body)
	}

	// The page size is clamped instead of letting a client dump everything.
	clamped, _ := getHistory(t, server, "?limit=9999")
	if clamped.Limit != maxHistoryPage {
		t.Fatalf("limit = %d, want %d", clamped.Limit, maxHistoryPage)
	}
}

func TestHistoryEndpointFiltersByResultAndTerm(t *testing.T) {
	st, server := newTestServer(t)
	seedHistory(t, st)

	failures, _ := getHistory(t, server, "?result=error&limit=50")
	if failures.Total != 3 {
		t.Fatalf("failed requests = %d, want 3", failures.Total)
	}
	for _, entry := range failures.History {
		if entry.Error == nil {
			t.Fatalf("result=error returned a success: %#v", entry)
		}
	}

	successes, _ := getHistory(t, server, "?result=ok&limit=50")
	if successes.Total != 2 {
		t.Fatalf("successful requests = %d, want 2", successes.Total)
	}

	// The term matches the error text as well as the model name.
	matched, _ := getHistory(t, server, "?q=exploded+2&limit=50")
	if matched.Total != 1 || matched.History[0].TS != 1002 {
		t.Fatalf("term search = %#v", matched)
	}
	none, body := getHistory(t, server, "?q=does-not-exist")
	if none.Total != 0 || len(none.History) != 0 || !strings.Contains(body, `"history":[]`) {
		t.Fatalf("unmatched term = %s", body)
	}
}
