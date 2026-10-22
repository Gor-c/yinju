package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTavilyProviderParsesResults(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"消防规范","url":"https://example.gov/a","content":"摘要内容"}]}`))
	}))
	defer srv.Close()

	client, err := New(Config{Enabled: true, Provider: "tavily", APIKey: "k", BaseURL: srv.URL}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.Search(context.Background(), "消防规范", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "消防规范" || results[0].Snippet != "摘要内容" {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Site != "example.gov" {
		t.Fatalf("site = %q", results[0].Site)
	}
	if gotPath != "/search" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["api_key"] != "k" {
		t.Fatalf("api key was not sent: %#v", gotBody)
	}
}

func TestBochaProviderParsesResults(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"webPages":{"value":[{"name":"标题","url":"https://www.mem.gov.cn/x","snippet":"片段","siteName":"mem.gov.cn","dateLastCrawled":"2026-01-02"}]}}}`))
	}))
	defer srv.Close()

	client, err := New(Config{Enabled: true, Provider: "bocha", APIKey: "k", BaseURL: srv.URL}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.Search(context.Background(), "检查要点", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "标题" || results[0].Site != "mem.gov.cn" {
		t.Fatalf("results = %#v", results)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("authorization = %q", gotAuth)
	}
}

func TestBraveProviderParsesResults(t *testing.T) {
	var gotQuery, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		gotToken = r.Header.Get("X-Subscription-Token")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"T","url":"https://news.example.com/p","description":"D","age":"3d","meta_url":{"hostname":"news.example.com"}}]}}`))
	}))
	defer srv.Close()

	client, err := New(Config{Enabled: true, Provider: "brave", APIKey: "k", BaseURL: srv.URL}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.Search(context.Background(), "最新要求", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].PublishedAt != "3d" {
		t.Fatalf("results = %#v", results)
	}
	if gotQuery != "最新要求" || gotToken != "k" {
		t.Fatalf("query = %q, token = %q", gotQuery, gotToken)
	}
}

func TestSearchAppliesDomainPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"title":"A","url":"https://gov.example.cn/a","content":"a"},
			{"title":"B","url":"https://spam.example.com/b","content":"b"},
			{"title":"C","url":"javascript:alert(1)","content":"c"}
		]}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		Enabled:      true,
		Provider:     "tavily",
		APIKey:       "k",
		BaseURL:      srv.URL,
		AllowDomains: []string{"example.cn"},
		DenyDomains:  []string{"spam.example.com"},
	}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.Search(context.Background(), "q", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "A" {
		t.Fatalf("results = %#v", results)
	}
}

func TestSearchCachesResults(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"results":[{"title":"A","url":"https://a.example.com/1","content":"x"}]}`))
	}))
	defer srv.Close()

	client, err := New(Config{
		Enabled:      true,
		Provider:     "tavily",
		APIKey:       "k",
		BaseURL:      srv.URL,
		CacheTTL:     time.Minute,
		CacheEntries: 10,
	}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(context.Background(), "缓存测试", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(context.Background(), "缓存测试", 5); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (second call should hit the cache)", calls)
	}
}

func TestSearchReportsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"invalid key"}`))
	}))
	defer srv.Close()

	client, err := New(Config{Enabled: true, Provider: "tavily", APIKey: "k", BaseURL: srv.URL}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Search(context.Background(), "q", 5)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRejectsMissingKeyAndProvider(t *testing.T) {
	if _, err := New(Config{Enabled: true, Provider: "tavily"}, nil); err == nil {
		t.Fatal("expected an error for a missing API key")
	}
	if _, err := New(Config{Enabled: true, Provider: "nope", APIKey: "k"}, nil); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if _, err := New(Config{Enabled: false}, nil); err == nil {
		t.Fatal("expected an error when disabled")
	}
}

func TestHostOfRejectsNonHTTP(t *testing.T) {
	if got := hostOf("javascript:alert(1)"); got != "" {
		t.Fatalf("host = %q, want empty", got)
	}
	if got := hostOf("https://user:pass@example.com/p"); got != "example.com" {
		t.Fatalf("host = %q", got)
	}
}
