package httpapi

import (
	"context"
	"log"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// record persists one request. The admitting key travels in the context so the
// store can charge the spend of an issued key in the same journal entry; the
// console and master-key traffic stamp nothing and stay uncapped.
func (s *Server) record(ctx context.Context, entry model.HistoryEntry) {
	entry = stampCallerKey(ctx, entry)
	if err := s.store.Record(entry); err != nil {
		log.Printf("persist request history: %v", err)
	}
}
