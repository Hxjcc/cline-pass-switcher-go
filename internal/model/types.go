package model

import (
	"bytes"
	"encoding/json"
)

const DefaultUpstreamBase = "https://api.cline.bot/api/v1"

// HistoryLimit is how many request records a data directory keeps. The console
// pages through them; the oldest entry beyond this is dropped on write.
const HistoryLimit = 500

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
}

type Config struct {
	Port   int    `json:"port"`
	APIKey string `json:"apiKey,omitempty"`
	// ProxyKey is the master client key: it opens the OpenAI-compatible
	// endpoints. It also opens the console until AdminKey is set, so a single
	// machine keeps working exactly as before.
	ProxyKey string `json:"proxyKey"`
	// AdminKey protects the console and the management API. Leaving it empty
	// falls back to ProxyKey; setting it takes the client key out of the
	// console entirely, which is what a shared deployment wants.
	AdminKey string `json:"adminKey,omitempty"`
	// ProxyKeys are additional client keys issued from the console. Each may be
	// pinned to one account and capped at a spend limit.
	ProxyKeys     []ProxyKeyGrant `json:"proxyKeys,omitempty"`
	PublicBaseURL string          `json:"publicBaseUrl"`
	// TrustedProxies lists IP addresses or CIDR blocks whose forwarded client
	// address headers (X-Forwarded-For / X-Real-IP) may be trusted. They decide
	// whether a request to a surface without a key came from this machine, and
	// which client a failed credential is charged to by the auth throttle.
	TrustedProxies []string `json:"trustedProxies,omitempty"`
	// TrustLocalPortForward declares that a non-loopback peer only reaches
	// this listener through a host port mapping bound to the host loopback
	// interface (docker `-p 127.0.0.1:3123:3123`). A container cannot see the
	// real client address, so the operator has to state that boundary
	// explicitly instead of the server guessing it from the Host header.
	TrustLocalPortForward bool `json:"trustLocalPortForward,omitempty"`
	// StrictToolHistory keeps the strict every-tool-result-has-a-call rule.
	// It is off by default so desktop clients can replay incomplete history.
	StrictToolHistory bool `json:"strictToolHistory,omitempty"`
	// WebSearchUpstream maps the hosted web_search tool onto a gateway
	// provider tool such as vercel:exa_search. Empty disables the mapping.
	WebSearchUpstream string `json:"webSearchUpstream,omitempty"`
	// WebFetchUpstream declares a gateway tool that reads a URL the user
	// pasted, for example vercel:browserbase_fetch. Empty disables it.
	WebFetchUpstream string `json:"webFetchUpstream,omitempty"`
	// ShellCompat restricts forwarded tool schemas that declare a "shell"
	// parameter (exec_command and friends) to this value and marks it
	// required, for example "powershell" on a Windows client. Empty or "off"
	// leaves the client's schemas untouched.
	ShellCompat string `json:"shellCompat,omitempty"`
	// ShellCompatEnforce also rewrites the model's actual tool-call arguments
	// for shell-capable tools. It is off by default and only makes sense when
	// ShellCompat is set.
	ShellCompatEnforce bool `json:"shellCompatEnforce,omitempty"`
	// CompactionRecentTokens is the verbatim tail kept inside a compaction
	// item (estimated tokens). The generated summary then only covers the
	// older part of the conversation, so recent paths, commands and errors
	// survive the compaction exactly. Zero disables the verbatim tail.
	CompactionRecentTokens int `json:"compactionRecentTokens,omitempty"`
	// CompactionReasoningEffort is the reasoning level compaction turns run at
	// (one of the model's advertised levels, or "auto" to pick the closest to
	// high). Reasoning models that starve on a smaller level answer "empty
	// response content", which costs a wasted pass plus a retry.
	CompactionReasoningEffort string `json:"compactionReasoningEffort,omitempty"`
	// CompactionMinOutputTokens is the output floor for compaction turns.
	CompactionMinOutputTokens int       `json:"compactionMinOutputTokens,omitempty"`
	UpstreamBase              string    `json:"upstreamBase"`
	Accounts                  []Account `json:"accounts"`
	AccountMode               string    `json:"accountMode"`
	ActiveAccount             int       `json:"activeAccount"`
	KnownModels               []string  `json:"knownModels"`
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

// ModelMeta carries what is known about one model. The capability fields at the
// top mirror the models.dev row for it, so they are kept whole even when only
// part of the row has a reader: ContextWindow caps the input tokens reported to
// the client and ReasoningEfforts picks the upstream effort, the console renders
// Reasoning, ReasoningEfforts, InputModalities, ContextWindow and OutputLimit,
// and DisplayName, Description, Family, Attachment, ToolCall, StructuredOutput,
// Temperature, OutputModalities, CapabilitiesKnown and CapabilityUpdatedAt are
// stored for later use.
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
	Pinnable            *bool                     `json:"pinnable,omitempty"`
	PinReason           string                    `json:"pinReason,omitempty"`
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

// GatewayAttempt is one provider attempt the Cline gateway reports it made,
// read from provider_metadata.gateway.routing.modelAttempts. The proxy's own
// Trace covers the attempts it drove; this one covers what happened inside
// the gateway after the request was accepted.
type GatewayAttempt struct {
	Provider   string `json:"provider,omitempty"`
	Status     int    `json:"status,omitempty"`
	MS         int64  `json:"ms,omitempty"`
	Success    bool   `json:"success,omitempty"`
	RequestID  string `json:"requestId,omitempty"`
	ResponseID string `json:"responseId,omitempty"`
}

type UsageStats struct {
	PromptTokens     int64    `json:"promptTokens,omitempty"`
	CompletionTokens int64    `json:"completionTokens,omitempty"`
	ReasoningTokens  int64    `json:"reasoningTokens,omitempty"`
	CachedTokens     int64    `json:"cachedTokens,omitempty"`
	TotalTokens      int64    `json:"totalTokens,omitempty"`
	Cost             *float64 `json:"cost,omitempty"`
	// The gateway splits its own bill per model leg; Cost above stays the
	// ledger number (the only one spend limits use), while these describe
	// where that number and any tool fees came from. CacheHitTokens and
	// CacheMissTokens are the provider's own prompt-cache counters, which are
	// more precise than the cached_tokens detail on CachedTokens.
	InputCost       *float64 `json:"inputCost,omitempty"`
	OutputCost      *float64 `json:"outputCost,omitempty"`
	SurchargeCost   *float64 `json:"surchargeCost,omitempty"`
	GatewayCost     *float64 `json:"gatewayCost,omitempty"`
	CacheHitTokens  int64    `json:"cacheHitTokens,omitempty"`
	CacheMissTokens int64    `json:"cacheMissTokens,omitempty"`
}

// FallbackReason values recorded when a request did not run on the channel
// the pin or the session affinity asked for.
const (
	// FallbackRetry: the wanted channel was attempted and failed first.
	FallbackRetry = "retry"
	// FallbackIgnored: the wanted channel never appears in the gateway's
	// attempt list, so the preference was ignored rather than failed over.
	FallbackIgnored = "ignored"
)

type HistoryEntry struct {
	TS        int64  `json:"ts"`
	Model     string `json:"model"`
	Provider  string `json:"provider,omitempty"`
	Canonical string `json:"canonical,omitempty"`
	// Resolved is the provider the Cline gateway reports it actually ran
	// (routing.resolvedProvider). It matches Provider except when the gateway
	// quietly rerouted a request away from the requested channel.
	Resolved string `json:"resolved,omitempty"`
	// Fallback marks a request that did not land on the channel the session
	// affinity or the model's pin asked for.
	Fallback bool `json:"fallback,omitempty"`
	// FallbackReason tells the two cases apart: the wanted channel was tried
	// and failed (FallbackRetry), or it was never tried at all
	// (FallbackIgnored), which is what a gateway that does not read channel
	// preferences looks like. Empty means the upstream reported too little to
	// decide.
	FallbackReason  string      `json:"fallbackReason,omitempty"`
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
	// AccountID is the stable identity behind Account. Per-account counters
	// are keyed by it so renaming an account keeps its statistics.
	AccountID string `json:"accountId,omitempty"`
	// KeyID / KeyName identify the issued client key that carried the request.
	// Both stay empty for console and master-key traffic; the store uses KeyID
	// to accumulate the spend a shared key is allowed to burn.
	KeyID    string   `json:"keyId,omitempty"`
	KeyName  string   `json:"keyName,omitempty"`
	Attempts []string `json:"attempts,omitempty"`
	Trace    []Trace  `json:"trace,omitempty"`
	// GatewayAttempts and GenerationID come from the gateway's routing
	// metadata and describe the work done inside the gateway for this request.
	GatewayAttempts []GatewayAttempt `json:"gatewayAttempts,omitempty"`
	GenerationID    string           `json:"generationId,omitempty"`
	// MissingSummarySections names the anchored summary sections a completed
	// compaction left out. The compaction still succeeded, so this is an
	// advisory note for the console rather than an error.
	MissingSummarySections []string `json:"missingSummarySections,omitempty"`
	// Degraded marks a compaction whose summary could not be produced: the
	// client received a fallback item so the session could continue, and the
	// console shows that this turn is not a real summary.
	Degraded bool `json:"degraded,omitempty"`
	// DegradeReason is the short failure text carried by that fallback item.
	DegradeReason string `json:"degradeReason,omitempty"`
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
	StoreSequence     uint64                  `json:"storeSequence,omitempty"`
	Models            map[string]ModelMeta    `json:"models"`
	History           []HistoryEntry          `json:"history"`
	ORModels          []string                `json:"orModelList"`
	ORModelsFetchedAt int64                   `json:"orModelsFetchedAt"`
	Stats             map[string]AccountStats `json:"stats"`
	// KeyUsage accumulates the spend each issued client key caused, keyed by
	// grant ID. It survives restarts so a spend limit cannot be reset by
	// restarting the proxy.
	KeyUsage            map[string]KeyUsage `json:"keyUsage,omitempty"`
	OfficialModelsFetch *OfficialFetch      `json:"officialModelsFetch,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		Port:                      3123,
		ProxyKey:                  "",
		PublicBaseURL:             "",
		CompactionRecentTokens:    DefaultCompactionRecentTokens,
		CompactionReasoningEffort: DefaultCompactionReasoningEffort,
		CompactionMinOutputTokens: DefaultCompactionMinOutputTokens,
		UpstreamBase:              DefaultUpstreamBase,
		Accounts:                  []Account{},
		AccountMode:               "single",
		ActiveAccount:             0,
		KnownModels:               append([]string(nil), DefaultKnownModels...),
		PerModel:                  map[string]PerModelConfig{},
	}
}

// DefaultCompactionRecentTokens keeps the last few turns verbatim inside a
// compaction item: enough for exact paths, commands and error text (a coding
// turn can easily spend 1-2k tokens of dialogue) without eating the context the
// compaction just freed. It is ~1.6% of a 1M window and only user/assistant
// text is charged against it.
const DefaultCompactionRecentTokens = 16000

// DefaultCompactionReasoningEffort runs compaction at the model's strongest
// level by default: reasoning models that are capped lower tend to spend the
// whole output budget thinking and answer "empty response content", which
// wastes a pass and a retry. "auto" selects the level closest to high.
const DefaultCompactionReasoningEffort = "max"

// DefaultCompactionMinOutputTokens gives a compaction summary enough room to
// finish after the model's hidden thinking. Hidden reasoning is charged to the
// same budget, and summaries routinely run to 5-6k visible tokens, so 8192
// still left the first pass truncating often enough to pay for a retry; the
// floor is two steps of 8192 instead, which lets the escalation double once
// more into the 32768 ceiling.
const DefaultCompactionMinOutputTokens = 16384

func EmptyMetadata() Metadata {
	return Metadata{
		Models:   map[string]ModelMeta{},
		History:  []HistoryEntry{},
		Stats:    map[string]AccountStats{},
		KeyUsage: map[string]KeyUsage{},
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
