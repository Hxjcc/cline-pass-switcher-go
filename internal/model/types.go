package model

import (
	"bytes"
	"encoding/json"
)

const DefaultUpstreamBase = "https://api.cline.bot/api/v1"

var DefaultKnownModels = []string{
	"cline-pass/glm-5.3-flash",
	"cline-pass/kimi-k3",
	"cline-pass/deepseek-v4-flash",
	"cline-pass/deepseek-v4.1-flash",
	"cline-pass/qwen3.8-max",
	"cline-pass/minimax-m3",
	"cline-pass/glm-5.3",
	"cline-pass/glm-5.2",
	"cline-pass/deepseek-v4-pro",
	"cline-pass/mimo-v2.5-pro",
	"cline-pass/mimo-v2.5",
	"cline-pass/kimi-k2.6",
	"cline-pass/qwen3.7-plus",
	"cline-pass/kimi-k2.7-code",
	"cline-pass/qwen3.7-max",
}

type Account struct {
	// ID identifies one account across renames and reordering. It is assigned
	// on load for configurations written before the field existed.
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

func (a *Account) UnmarshalJSON(data []byte) error {
	type accountAlias struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Key     string `json:"key"`
		Enabled *bool  `json:"enabled"`
	}
	var raw accountAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.ID = raw.ID
	a.Name = raw.Name
	a.Key = raw.Key
	a.Enabled = true
	if raw.Enabled != nil {
		a.Enabled = *raw.Enabled
	}
	return nil
}

type PerModelConfig struct {
	Upstream  string   `json:"upstream,omitempty"`
	Upstreams []string `json:"upstreams"`
	Exclude   []string `json:"exclude"`
	PinMode   string   `json:"pinMode"`
	Sort      *string  `json:"sort"`
}

type Config struct {
	Port          int    `json:"port"`
	APIKey        string `json:"apiKey,omitempty"`
	ProxyKey      string `json:"proxyKey"`
	PublicBaseURL string `json:"publicBaseUrl"`
	ExposeCatalog bool   `json:"exposeCatalog"`
	// StrictToolHistory keeps the strict every-tool-result-has-a-call rule.
	// It is off by default so desktop clients can replay incomplete history.
	StrictToolHistory bool `json:"strictToolHistory,omitempty"`
	// WebSearchUpstream maps the hosted web_search tool onto a gateway
	// provider tool such as vercel:exa_search. Empty disables the mapping.
	WebSearchUpstream string `json:"webSearchUpstream,omitempty"`
	// WebSearchDirectAPIKey turns on proxy-executed web search: the hosted
	// web_search declaration becomes a function tool the proxy answers itself
	// with a search API call, so clients can be shown the real queries and
	// pages as web_search_call items. Empty keeps the gateway mapping.
	WebSearchDirectAPIKey string `json:"webSearchDirectApiKey,omitempty"`
	// WebSearchDirectBaseURL overrides the search API endpoint (default Exa).
	WebSearchDirectBaseURL string `json:"webSearchDirectBaseUrl,omitempty"`
	// WebFetchUpstream declares a gateway tool that reads a URL the user
	// pasted, for example vercel:browserbase_fetch. Empty disables it.
	WebFetchUpstream string    `json:"webFetchUpstream,omitempty"`
	UpstreamBase     string    `json:"upstreamBase"`
	Accounts         []Account `json:"accounts"`
	AccountMode      string    `json:"accountMode"`
	ActiveAccount    int       `json:"activeAccount"`
	KnownModels      []string  `json:"knownModels"`
	// Models the user removed from the subscription list. The official
	// catalog fetch skips these so a deletion is not undone on the next sync;
	// a successful live request re-subscribes the model.
	RemovedModels []string                  `json:"removedModels,omitempty"`
	PerModel      map[string]PerModelConfig `json:"perModel"`
}

type UpstreamDetail struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Endpoints int    `json:"endpoints"`
	Context   int    `json:"context"`
	Uptime    int    `json:"uptime"`
}

type UpstreamStatus struct {
	Status    string `json:"status"`
	Note      string `json:"note"`
	CheckedAt int64  `json:"checkedAt"`
	// Round trip of the validation request. Zero when the status was learned
	// from a failed live request rather than a check.
	MS int64 `json:"ms,omitempty"`
}

type ModelMeta struct {
	OK                  bool                      `json:"ok,omitempty"`
	DisplayName         string                    `json:"displayName,omitempty"`
	Description         string                    `json:"description,omitempty"`
	Family              string                    `json:"family,omitempty"`
	CapabilitiesKnown   bool                      `json:"capabilitiesKnown,omitempty"`
	Reasoning           bool                      `json:"reasoning,omitempty"`
	ReasoningEfforts    []string                  `json:"reasoningEfforts,omitempty"`
	InputModalities     []string                  `json:"inputModalities,omitempty"`
	OutputModalities    []string                  `json:"outputModalities,omitempty"`
	Attachment          bool                      `json:"attachment,omitempty"`
	ToolCall            bool                      `json:"toolCall,omitempty"`
	StructuredOutput    bool                      `json:"structuredOutput,omitempty"`
	Temperature         bool                      `json:"temperature,omitempty"`
	ContextWindow       int                       `json:"contextWindow,omitempty"`
	OutputLimit         int                       `json:"outputLimit,omitempty"`
	CapabilityUpdatedAt int64                     `json:"capabilityUpdatedAt,omitempty"`
	Pipeline            string                    `json:"pipeline,omitempty"`
	Pinnable            bool                      `json:"pinnable,omitempty"`
	AvailableProviders  []string                  `json:"availableProviders,omitempty"`
	CanonicalSlug       string                    `json:"canonicalSlug,omitempty"`
	OpenRouterSlug      string                    `json:"openrouterSlug,omitempty"`
	UpstreamDetail      map[string]UpstreamDetail `json:"upstreamDetail,omitempty"`
	Upstreams           []string                  `json:"upstreams,omitempty"`
	Tier0               []string                  `json:"tier0,omitempty"`
	LastProvider        string                    `json:"lastProvider,omitempty"`
	LastMS              int64                     `json:"lastMs,omitempty"`
	ProbedAt            int64                     `json:"probedAt,omitempty"`
	UpstreamStatus      map[string]UpstreamStatus `json:"upstreamStatus,omitempty"`
	ValidatedAt         int64                     `json:"validatedAt,omitempty"`
}

type Trace struct {
	Upstream string `json:"upstream,omitempty"`
	Status   int    `json:"status"`
	MS       int64  `json:"ms"`
	Note     string `json:"note,omitempty"`
}

type UsageStats struct {
	PromptTokens     int64    `json:"promptTokens,omitempty"`
	CompletionTokens int64    `json:"completionTokens,omitempty"`
	ReasoningTokens  int64    `json:"reasoningTokens,omitempty"`
	CachedTokens     int64    `json:"cachedTokens,omitempty"`
	TotalTokens      int64    `json:"totalTokens,omitempty"`
	Cost             *float64 `json:"cost,omitempty"`
}

type HistoryEntry struct {
	TS              int64       `json:"ts"`
	Model           string      `json:"model"`
	Provider        string      `json:"provider,omitempty"`
	Canonical       string      `json:"canonical,omitempty"`
	MS              int64       `json:"ms"`
	TTFTMs          int64       `json:"ttftMs,omitempty"`
	Stream          bool        `json:"stream"`
	Kind            string      `json:"kind,omitempty"`
	Effort          string      `json:"effort,omitempty"`
	RequestedEffort string      `json:"requestedEffort,omitempty"`
	FinishReason    string      `json:"finishReason,omitempty"`
	Usage           *UsageStats `json:"usage,omitempty"`
	Error           *string     `json:"error"`
	Account         string      `json:"account,omitempty"`
	Attempts        []string    `json:"attempts,omitempty"`
	Trace           []Trace     `json:"trace,omitempty"`
}

type AccountStats struct {
	Requests  int64   `json:"requests"`
	LastUsed  int64   `json:"lastUsed"`
	LastError *string `json:"lastError"`
}

type OfficialFetch struct {
	TS      int64    `json:"ts"`
	Sources []string `json:"sources"`
	Found   int      `json:"found"`
	Added   []string `json:"added"`
	Total   int      `json:"total"`
}

type Metadata struct {
	// Last durable journal entry included in this snapshot.
	StoreSequence       uint64                  `json:"storeSequence,omitempty"`
	Models              map[string]ModelMeta    `json:"models"`
	History             []HistoryEntry          `json:"history"`
	Catalog             []string                `json:"catalog"`
	CatalogFetchedAt    int64                   `json:"catalogFetchedAt"`
	ORModels            []string                `json:"orModelList"`
	ORModelsFetchedAt   int64                   `json:"orModelsFetchedAt"`
	Stats               map[string]AccountStats `json:"stats"`
	OfficialModelsFetch *OfficialFetch          `json:"officialModelsFetch,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Port:          3123,
		ProxyKey:      "",
		PublicBaseURL: "",
		ExposeCatalog: false,
		UpstreamBase:  DefaultUpstreamBase,
		Accounts:      []Account{},
		AccountMode:   "single",
		ActiveAccount: 0,
		KnownModels:   append([]string(nil), DefaultKnownModels...),
		PerModel:      map[string]PerModelConfig{},
	}
}

func EmptyMetadata() Metadata {
	return Metadata{
		Models:  map[string]ModelMeta{},
		History: []HistoryEntry{},
		Catalog: []string{},
		Stats:   map[string]AccountStats{},
	}
}

func Clone[T any](value T) T {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var clone T
	if err := json.Unmarshal(raw, &clone); err != nil {
		return value
	}
	return clone
}

func DecodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}
