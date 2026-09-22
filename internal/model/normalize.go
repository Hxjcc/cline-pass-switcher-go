package model

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/strx"
)

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := DecodeStrict(raw, &cfg); err != nil {
			return Config{}, err
		}
	} else if !os.IsNotExist(err) {
		return Config{}, err
	}
	ApplyEnvironment(&cfg)
	NormalizeConfig(&cfg)
	return cfg, nil
}

func LoadMetadata(path string) (Metadata, error) {
	meta := EmptyMetadata()
	raw, err := os.ReadFile(path)
	if err == nil {
		if err := DecodeStrict(raw, &meta); err != nil {
			return Metadata{}, err
		}
	} else if !os.IsNotExist(err) {
		return Metadata{}, err
	}
	NormalizeMetadata(&meta)
	return meta, nil
}

func ApplyEnvironment(cfg *Config) {
	if key := strings.TrimSpace(os.Getenv("CLINE_PASS_KEY")); key != "" {
		found := false
		for _, account := range cfg.Accounts {
			if account.Key == key {
				found = true
				break
			}
		}
		if !found {
			cfg.Accounts = append([]Account{{Name: "env-account", Key: key, Enabled: true}}, cfg.Accounts...)
		}
	}
	if key := strings.TrimSpace(os.Getenv("PROXY_KEY")); key != "" {
		cfg.ProxyKey = key
	}
	if baseURL := strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")); baseURL != "" {
		cfg.PublicBaseURL = strings.TrimRight(baseURL, "/")
	}
	if rawPort := strings.TrimSpace(os.Getenv("PORT")); rawPort != "" {
		if port, err := strconv.Atoi(rawPort); err == nil && port > 0 && port <= 65535 {
			cfg.Port = port
		}
	}
	if proxies := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); proxies != "" {
		cfg.TrustedProxies = strings.Split(proxies, ",")
	}
	if rawTrust := strings.TrimSpace(os.Getenv("TRUST_LOCAL_PORT_FORWARD")); rawTrust != "" {
		if trust, err := strconv.ParseBool(rawTrust); err == nil {
			cfg.TrustLocalPortForward = trust
		}
	}
	if rawStrict := strings.TrimSpace(os.Getenv("STRICT_TOOL_HISTORY")); rawStrict != "" {
		if strict, err := strconv.ParseBool(rawStrict); err == nil {
			cfg.StrictToolHistory = strict
		}
	}
	if upstream := strings.TrimSpace(os.Getenv("WEB_SEARCH_UPSTREAM")); upstream != "" {
		cfg.WebSearchUpstream = upstream
	}
	if upstream := strings.TrimSpace(os.Getenv("WEB_FETCH_UPSTREAM")); upstream != "" {
		cfg.WebFetchUpstream = upstream
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL_COMPAT")); shell != "" {
		cfg.ShellCompat = shell
	}
	if raw := strings.TrimSpace(os.Getenv("SHELL_COMPAT_ENFORCE")); raw != "" {
		if enforce, err := strconv.ParseBool(raw); err == nil {
			cfg.ShellCompatEnforce = enforce
		}
	}
}

func NormalizeConfig(cfg *Config) {
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = 3123
	}
	if strings.TrimSpace(cfg.UpstreamBase) == "" {
		cfg.UpstreamBase = DefaultUpstreamBase
	}
	cfg.UpstreamBase = strings.TrimRight(strings.TrimSpace(cfg.UpstreamBase), "/")
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	cfg.ProxyKey = strings.TrimSpace(cfg.ProxyKey)
	cfg.ShellCompat = strings.TrimSpace(cfg.ShellCompat)

	// The top-level apiKey is a legacy field. It is folded into the account
	// pool exactly once and then dropped, so it can never come back as a
	// runtime fallback that ignores a disabled or removed account.
	if len(cfg.Accounts) == 0 && strings.TrimSpace(cfg.APIKey) != "" {
		cfg.Accounts = []Account{{Name: "默认账号", Key: strings.TrimSpace(cfg.APIKey), Enabled: true}}
		cfg.AccountMode = "single"
		cfg.ActiveAccount = 0
	}
	cfg.APIKey = ""
	cfg.TrustedProxies = strx.UniqueTrimmed(cfg.TrustedProxies)
	if cfg.AccountMode != "roundrobin" {
		cfg.AccountMode = "single"
	}
	if cfg.ActiveAccount < 0 {
		cfg.ActiveAccount = 0
	}
	if len(cfg.Accounts) == 0 {
		cfg.ActiveAccount = 0
	} else if cfg.ActiveAccount >= len(cfg.Accounts) {
		cfg.ActiveAccount = len(cfg.Accounts) - 1
	}
	if cfg.Accounts == nil {
		cfg.Accounts = []Account{}
	}
	// Identities are assigned once and then preserved, so a client can send an
	// account back with an empty key without losing which key belongs to it.
	seenIDs := make(map[string]struct{}, len(cfg.Accounts))
	for index := range cfg.Accounts {
		account := &cfg.Accounts[index]
		if account.ID == "" {
			account.ID = NewAccountID()
		}
		if _, duplicate := seenIDs[account.ID]; duplicate {
			account.ID = NewAccountID()
		}
		seenIDs[account.ID] = struct{}{}
	}
	if cfg.KnownModels == nil {
		cfg.KnownModels = append([]string(nil), DefaultKnownModels...)
	}
	cfg.KnownModels = strx.UniqueTrimmed(cfg.KnownModels)
	// A model that is subscribed again (live request, explicit re-add) wins
	// over an earlier removal.
	known := make(map[string]struct{}, len(cfg.KnownModels))
	for _, id := range cfg.KnownModels {
		known[id] = struct{}{}
	}
	removed := make([]string, 0, len(cfg.RemovedModels))
	for _, id := range strx.UniqueTrimmed(cfg.RemovedModels) {
		if _, found := known[id]; !found {
			removed = append(removed, id)
		}
	}
	cfg.RemovedModels = removed
	if cfg.PerModel == nil {
		cfg.PerModel = map[string]PerModelConfig{}
	}
	for id, modelConfig := range cfg.PerModel {
		if modelConfig.Upstreams == nil && modelConfig.Upstream != "" {
			modelConfig.Upstreams = []string{modelConfig.Upstream}
		}
		modelConfig.Upstreams = strx.UniqueTrimmed(modelConfig.Upstreams)
		modelConfig.Exclude = strx.UniqueTrimmed(modelConfig.Exclude)
		excluded := make(map[string]struct{}, len(modelConfig.Exclude))
		for _, upstream := range modelConfig.Exclude {
			excluded[upstream] = struct{}{}
		}
		filtered := make([]string, 0, len(modelConfig.Upstreams))
		for _, upstream := range modelConfig.Upstreams {
			if _, found := excluded[upstream]; !found {
				filtered = append(filtered, upstream)
			}
		}
		modelConfig.Upstreams = filtered
		if len(modelConfig.Upstreams) > 10 {
			modelConfig.Upstreams = modelConfig.Upstreams[:10]
		}
		if len(modelConfig.Exclude) > 10 {
			modelConfig.Exclude = modelConfig.Exclude[:10]
		}
		if modelConfig.PinMode != "preferred" {
			modelConfig.PinMode = "strict"
		}
		if modelConfig.Sort != nil {
			switch *modelConfig.Sort {
			case "cost", "ttft", "tps":
			default:
				modelConfig.Sort = nil
			}
		}
		modelConfig.Upstream = ""
		if len(modelConfig.Upstreams) > 0 {
			modelConfig.Upstream = modelConfig.Upstreams[0]
		}
		cfg.PerModel[id] = modelConfig
	}
}

func NormalizeMetadata(meta *Metadata) {
	if meta.Models == nil {
		meta.Models = map[string]ModelMeta{}
	}
	if meta.History == nil {
		meta.History = []HistoryEntry{}
	}
	if meta.Catalog == nil {
		meta.Catalog = []string{}
	}
	if meta.Stats == nil {
		meta.Stats = map[string]AccountStats{}
	}
	if len(meta.History) > HistoryLimit {
		meta.History = meta.History[:HistoryLimit]
	}
	if meta.OfficialModelsFetch != nil && meta.OfficialModelsFetch.Added == nil {
		meta.OfficialModelsFetch.Added = []string{}
	}
	for id, modelMeta := range meta.Models {
		if modelMeta.UpstreamDetail == nil {
			modelMeta.UpstreamDetail = map[string]UpstreamDetail{}
		}
		if modelMeta.UpstreamStatus == nil {
			modelMeta.UpstreamStatus = map[string]UpstreamStatus{}
		}
		meta.Models[id] = modelMeta
	}
}

func EnsureDataDir(dataDir string) error {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "."
	}
	return os.MkdirAll(filepath.Clean(dataDir), 0o755)
}
