package upstream

import (
	"log"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

func (s *Service) updateMetadata(update func(*model.Metadata)) {
	if err := s.store.UpdateMetadata(update); err != nil {
		log.Printf("persist upstream metadata: %v", err)
	}
}

func (s *Service) updateModelMeta(id string, update func(*model.ModelMeta)) {
	if _, err := s.store.UpdateModelMeta(id, update); err != nil {
		log.Printf("persist upstream model metadata: %v", err)
	}
}
