package httpapi

import (
	"net/http"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/store"
)

func (s *Server) handleUsage(writer http.ResponseWriter, request *http.Request) {
	q := request.URL.Query()
	report, err := s.store.UsageReport(store.UsageQuery{From: q.Get("from"), To: q.Get("to"), Model: q.Get("model"), Account: q.Get("account"), Key: q.Get("key")})
	if err != nil {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, report)
}
