package httpapi

import (
	"log"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func (s *Server) record(entry model.HistoryEntry) {
	if err := s.store.Record(entry); err != nil {
		log.Printf("persist request history: %v", err)
	}
}
