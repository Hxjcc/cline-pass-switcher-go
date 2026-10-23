package upstream

import (
	"context"
	"sync"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

func (s *Service) validateGatewayChannels(ctx context.Context, modelID string, cfg model.PerModelConfig) (ValidationResult, error) {
	if _, err := s.ReadGatewayCatalog(ctx, modelID, false); err != nil {
		return ValidationResult{}, err
	}
	channels := GatewayChannels(s.store.ModelMeta(modelID), cfg.GatewayHas)
	account := s.store.PickAccount()
	if account.Key == "" {
		return ValidationResult{}, errNoAccount
	}
	results := map[string]model.UpstreamStatus{}
	var mu sync.Mutex
	var workers sync.WaitGroup
	semaphore := make(chan struct{}, 3)
	for _, target := range channels {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				return
			}
			testCfg := cfg
			testCfg.Upstreams = []string{target}
			testCfg.Exclude = nil
			check, err := s.VerifyGatewayRouting(ctx, modelID, testCfg, account)
			status := "ok"
			note := check.Error
			if err != nil {
				status = "unknown"
				note = err.Error()
			} else if !check.OK {
				status = "bad"
			}
			mu.Lock()
			results[target] = model.UpstreamStatus{Status: status, Note: note, CheckedAt: check.CheckedAt, MS: check.MS}
			mu.Unlock()
		}()
	}
	workers.Wait()
	summary := map[string]int{"ok": 0, "limited": 0, "bad": 0, "auth": 0, "unknown": 0}
	for _, status := range results {
		summary[status.Status]++
	}
	return ValidationResult{Supported: true, Summary: summary, Results: results}, ctx.Err()
}
