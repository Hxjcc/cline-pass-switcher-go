// Package search runs web searches for the proxy's own hosted web_search
// tool. The proxy talks to a search API directly instead of delegating the
// search to the upstream gateway, so it knows the exact queries and the pages
// they returned and can report them back to the client.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultExaBaseURL is the public Exa API endpoint.
	DefaultExaBaseURL = "https://api.exa.ai"
	defaultTimeout    = 30 * time.Second
	// maxErrorMessage bounds how much of a failure body is quoted back.
	maxErrorMessage = 400
	// maxResponseBytes bounds how much of a successful response is read.
	maxResponseBytes = 8 << 20
	// maxResults caps the requested result count; providers reject more.
	maxResults = 10
	// defaultTextChars is how much page text is fetched per result. The text
	// is fed to the model, so it must stay small enough to fit a tool message.
	defaultTextChars = 1500
)

// Result is one page returned by a search provider.
type Result struct {
	Title string
	URL   string
	Text  string
}

// Provider executes one web search query.
type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// Exa is a Provider backed by https://exa.ai.
type Exa struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// NewExa builds a client. An empty baseURL selects the public Exa endpoint;
// tests and self-hosted gateways pass their own.
func NewExa(apiKey, baseURL string) *Exa {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultExaBaseURL
	}
	return &Exa{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: base,
		client:  &http.Client{Timeout: defaultTimeout},
	}
}

type exaRequest struct {
	Query      string       `json:"query"`
	NumResults int          `json:"numResults,omitempty"`
	Type       string       `json:"type,omitempty"`
	Contents   *exaContents `json:"contents,omitempty"`
}

type exaContents struct {
	Text *exaText `json:"text,omitempty"`
}

type exaText struct {
	MaxCharacters int `json:"maxCharacters,omitempty"`
}

type exaResponse struct {
	Results []exaResult `json:"results"`
}

type exaResult struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Text  string `json:"text"`
}

// Search implements Provider.
func (client *Exa) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("web search query is empty")
	}
	if client.apiKey == "" {
		return nil, fmt.Errorf("web search API key is not configured")
	}
	if limit <= 0 {
		limit = 6
	}
	if limit > maxResults {
		limit = maxResults
	}
	payload, err := json.Marshal(exaRequest{
		Query:      query,
		NumResults: limit,
		Type:       "auto",
		Contents:   &exaContents{Text: &exaText{MaxCharacters: defaultTextChars}},
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/search", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", client.apiKey)
	response, err := client.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorMessage))
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return nil, fmt.Errorf("web search returned HTTP %d: %s", response.StatusCode, message)
	}
	var decoded exaResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("web search response was not valid JSON: %w", err)
	}
	results := make([]Result, 0, len(decoded.Results))
	for _, entry := range decoded.Results {
		url := strings.TrimSpace(entry.URL)
		if url == "" {
			continue
		}
		results = append(results, Result{
			Title: strings.TrimSpace(entry.Title),
			URL:   url,
			Text:  strings.TrimSpace(entry.Text),
		})
	}
	return results, nil
}
