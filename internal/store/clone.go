package store

import (
	"maps"
	"slices"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/model"
)

// cloneConfig detaches every mutable slice and map in a configuration. JSON
// round-tripping was convenient but allocated far more than the few fields
// that actually change, especially on the per-request TTL path.
func cloneConfig(cfg model.Config) model.Config {
	cfg.ProxyKeys = slices.Clone(cfg.ProxyKeys)
	cfg.TrustedProxies = slices.Clone(cfg.TrustedProxies)
	cfg.Accounts = slices.Clone(cfg.Accounts)
	cfg.KnownModels = slices.Clone(cfg.KnownModels)
	cfg.RemovedModels = slices.Clone(cfg.RemovedModels)
	if cfg.PerModel != nil {
		perModel := make(map[string]model.PerModelConfig, len(cfg.PerModel))
		for id, value := range cfg.PerModel {
			value.Upstreams = slices.Clone(value.Upstreams)
			value.Exclude = slices.Clone(value.Exclude)
			value.GatewayHas = slices.Clone(value.GatewayHas)
			perModel[id] = value
		}
		cfg.PerModel = perModel
	}
	return cfg
}

func cloneMetadata(meta model.Metadata) model.Metadata {
	if meta.Models != nil {
		models := make(map[string]model.ModelMeta, len(meta.Models))
		for id, value := range meta.Models {
			models[id] = cloneModelMeta(value)
		}
		meta.Models = models
	}
	if meta.History != nil {
		history := make([]model.HistoryEntry, len(meta.History))
		for index, entry := range meta.History {
			history[index] = cloneHistoryEntry(entry)
		}
		meta.History = history
	}
	meta.ORModels = slices.Clone(meta.ORModels)
	if meta.Stats != nil {
		stats := make(map[string]model.AccountStats, len(meta.Stats))
		for id, value := range meta.Stats {
			if value.LastError != nil {
				message := *value.LastError
				value.LastError = &message
			}
			stats[id] = value
		}
		meta.Stats = stats
	}
	meta.KeyUsage = maps.Clone(meta.KeyUsage)
	if meta.OfficialModelsFetch != nil {
		fetch := *meta.OfficialModelsFetch
		fetch.Sources = slices.Clone(fetch.Sources)
		fetch.Added = slices.Clone(fetch.Added)
		meta.OfficialModelsFetch = &fetch
	}
	return meta
}

func cloneModelMeta(meta model.ModelMeta) model.ModelMeta {
	meta.GatewayStatusHas = slices.Clone(meta.GatewayStatusHas)
	if meta.GatewayCatalog != nil {
		catalog := *meta.GatewayCatalog
		catalog.Providers = slices.Clone(catalog.Providers)
		for i := range catalog.Providers {
			catalog.Providers[i].Has = slices.Clone(catalog.Providers[i].Has)
		}
		meta.GatewayCatalog = &catalog
	}
	if meta.RoutingChecks != nil {
		checks := make(map[string]model.RoutingCheck, len(meta.RoutingChecks))
		for key, check := range meta.RoutingChecks {
			check.Has = slices.Clone(check.Has)
			check.Targets = slices.Clone(check.Targets)
			check.Actual = slices.Clone(check.Actual)
			checks[key] = check
		}
		meta.RoutingChecks = checks
	}
	meta.ReasoningEfforts = slices.Clone(meta.ReasoningEfforts)
	meta.InputModalities = slices.Clone(meta.InputModalities)
	meta.OutputModalities = slices.Clone(meta.OutputModalities)
	meta.AvailableProviders = slices.Clone(meta.AvailableProviders)
	meta.Upstreams = slices.Clone(meta.Upstreams)
	meta.Tier0 = slices.Clone(meta.Tier0)
	meta.UpstreamDetail = maps.Clone(meta.UpstreamDetail)
	meta.UpstreamStatus = maps.Clone(meta.UpstreamStatus)
	if meta.Pinnable != nil {
		value := *meta.Pinnable
		meta.Pinnable = &value
	}
	return meta
}

func cloneHistoryEntry(entry model.HistoryEntry) model.HistoryEntry {
	entry.GatewayHas = slices.Clone(entry.GatewayHas)
	entry.TargetProviders = slices.Clone(entry.TargetProviders)
	entry.Attempts = slices.Clone(entry.Attempts)
	entry.Trace = slices.Clone(entry.Trace)
	entry.GatewayAttempts = slices.Clone(entry.GatewayAttempts)
	entry.MissingSummarySections = slices.Clone(entry.MissingSummarySections)
	if entry.Usage != nil {
		usage := *entry.Usage
		usage.Cost = cloneFloat(usage.Cost)
		usage.InputCost = cloneFloat(usage.InputCost)
		usage.OutputCost = cloneFloat(usage.OutputCost)
		usage.SurchargeCost = cloneFloat(usage.SurchargeCost)
		usage.GatewayCost = cloneFloat(usage.GatewayCost)
		entry.Usage = &usage
	}
	if entry.Error != nil {
		message := *entry.Error
		entry.Error = &message
	}
	return entry
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneJournalEntry(entry journalEntry) journalEntry {
	if entry.Config != nil {
		config := cloneConfig(*entry.Config)
		entry.Config = &config
	}
	if entry.Metadata != nil {
		metadata := cloneMetadata(*entry.Metadata)
		entry.Metadata = &metadata
	}
	if entry.ModelMeta != nil {
		meta := cloneModelMeta(*entry.ModelMeta)
		entry.ModelMeta = &meta
	}
	if entry.Record != nil {
		record := cloneHistoryEntry(*entry.Record)
		entry.Record = &record
	}
	return entry
}
