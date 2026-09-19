package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExaSearchSendsQueryAndParsesResults(t *testing.T) {
	var path, apiKey string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		path = request.URL.Path
		apiKey = request.Header.Get("x-api-key")
		raw, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(raw, &body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"results":[
			{"title":"  Example  ","url":"https://example.com/a","text":"  body  "},
			{"title":"no url","url":"","text":"skipped"}
		]}`)
	}))
	defer server.Close()

	client := NewExa("secret", server.URL+"/")
	results, err := client.Search(context.Background(), "  golang news  ", 4)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/search" {
		t.Fatalf("unexpected path: %s", path)
	}
	if apiKey != "secret" {
		t.Fatalf("API key was not forwarded: %q", apiKey)
	}
	if body["query"] != "golang news" || body["numResults"] != float64(4) || body["type"] != "auto" {
		t.Fatalf("unexpected request body: %#v", body)
	}
	contents, _ := body["contents"].(map[string]any)
	text, _ := contents["text"].(map[string]any)
	if text["maxCharacters"] == nil {
		t.Fatalf("page text was not requested: %#v", body)
	}
	if len(results) != 1 {
		t.Fatalf("entries without a URL should be dropped: %#v", results)
	}
	if results[0].Title != "Example" || results[0].URL != "https://example.com/a" || results[0].Text != "body" {
		t.Fatalf("results were not trimmed: %#v", results[0])
	}
}

func TestExaSearchReportsUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, `{"error":"bad key"}`)
	}))
	defer server.Close()

	client := NewExa("secret", server.URL)
	_, err := client.Search(context.Background(), "news", 3)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExaSearchValidatesInput(t *testing.T) {
	if _, err := NewExa("", "").Search(context.Background(), "news", 3); err == nil {
		t.Fatal("a missing API key must fail before the request")
	}
	if _, err := NewExa("secret", "").Search(context.Background(), "   ", 3); err == nil {
		t.Fatal("an empty query must fail before the request")
	}
}

func TestExaSearchClampsResultCount(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(raw, &body)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"results":[]}`)
	}))
	defer server.Close()

	if _, err := NewExa("secret", server.URL).Search(context.Background(), "news", 99); err != nil {
		t.Fatal(err)
	}
	if body["numResults"] != float64(maxResults) {
		t.Fatalf("result count was not clamped: %#v", body["numResults"])
	}
}
