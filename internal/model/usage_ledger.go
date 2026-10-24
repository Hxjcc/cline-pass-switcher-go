package model

// UsageTotals keeps upstream-reported usage separately from the bounded
// request history. Reasoning is part of output, not an extra token charge.
type UsageTotals struct {
	Requests        int64   `json:"requests"`
	Errors          int64   `json:"errors"`
	UsageRequests   int64   `json:"usageRequests"`
	CostRequests    int64   `json:"costRequests"`
	InputTokens     int64   `json:"inputTokens"`
	OutputTokens    int64   `json:"outputTokens"`
	ReasoningTokens int64   `json:"reasoningTokens"`
	CachedTokens    int64   `json:"cachedTokens"`
	TotalTokens     int64   `json:"totalTokens"`
	Cost            float64 `json:"cost"`
}

type UsageBucket struct {
	Day         string `json:"day"`
	Model       string `json:"model"`
	Account     string `json:"account"`
	Key         string `json:"key"`
	AccountName string `json:"accountName"`
	KeyName     string `json:"keyName"`
	UsageTotals
}

type UsageLedger struct {
	StartedAt        int64                  `json:"startedAt"`
	ImportedRequests int64                  `json:"importedRequests"`
	LatestDay        string                 `json:"latestDay"`
	Buckets          map[string]UsageBucket `json:"buckets"`
}
