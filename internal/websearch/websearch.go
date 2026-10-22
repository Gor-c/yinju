// Package websearch provides a pluggable web search client for the internal
// agent. Search results are treated as untrusted external content: callers must
// keep them clearly separated from knowledge-base citations.
package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"knowledge-base/internal/logging"
)

// Result is one search hit.
type Result struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Site        string `json:"site,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

// Provider is a search backend. Implementations must treat the API key as a
// secret and never include it in error messages.
type Provider interface {
	Search(ctx context.Context, query string, limit int) ([]Result, error)
}

// Config is the runtime configuration for the search client.
type Config struct {
	Enabled      bool
	Provider     string
	APIKey       string
	BaseURL      string
	Timeout      time.Duration
	CacheTTL     time.Duration
	CacheEntries int
	AllowDomains []string
	DenyDomains  []string
}

// Client searches through one provider, applies domain filters and caches
// results so repeated questions do not burn quota.
type Client struct {
	cfg      Config
	provider Provider
	cache    *resultCache
}

type cacheEntry struct {
	added   time.Time
	results []Result
}

type resultCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	items   map[string]cacheEntry
	order   []string
	clock   func() time.Time
	enabled bool
}

func newCache(ttl time.Duration, max int) *resultCache {
	if ttl <= 0 || max <= 0 {
		return &resultCache{enabled: false, clock: time.Now}
	}
	return &resultCache{enabled: true, ttl: ttl, max: max, items: map[string]cacheEntry{}, clock: time.Now}
}

func (c *resultCache) get(key string) ([]Result, bool) {
	if !c.enabled {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok || c.clock().Sub(entry.added) > c.ttl {
		return nil, false
	}
	return entry.results, true
}

func (c *resultCache) set(key string, results []Result) {
	if !c.enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; !exists {
		c.order = append(c.order, key)
	}
	c.items[key] = cacheEntry{added: c.clock(), results: results}
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.items, oldest)
	}
}

// New builds a client for the configured provider.
func New(cfg Config, httpClient *http.Client) (*Client, error) {
	if !cfg.Enabled {
		return nil, errors.New("web search is disabled")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("web search provider %q requires an API key", cfg.Provider)
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	var provider Provider
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "tavily":
		provider = &tavilyProvider{base: defaultBase(cfg.BaseURL, "https://api.tavily.com"), key: cfg.APIKey, http: httpClient}
	case "bocha":
		provider = &bochaProvider{base: defaultBase(cfg.BaseURL, "https://api.bochaai.com/v1"), key: cfg.APIKey, http: httpClient}
	case "brave":
		provider = &braveProvider{base: defaultBase(cfg.BaseURL, "https://api.search.brave.com/res/v1"), key: cfg.APIKey, http: httpClient}
	case "":
		return nil, errors.New("WEB_SEARCH_PROVIDER is not set")
	default:
		return nil, fmt.Errorf("unsupported web search provider: %s", cfg.Provider)
	}
	return &Client{cfg: cfg, provider: provider, cache: newCache(cfg.CacheTTL, cfg.CacheEntries)}, nil
}

// FromEnv builds a client from environment variables. It returns (nil, nil)
// when web search is not enabled, which callers treat as "feature off".
func FromEnv() (*Client, error) {
	if !envFlag("WEB_SEARCH_ENABLED", false) {
		return nil, nil
	}
	cfg := Config{
		Enabled:      true,
		Provider:     os.Getenv("WEB_SEARCH_PROVIDER"),
		APIKey:       os.Getenv("WEB_SEARCH_API_KEY"),
		BaseURL:      os.Getenv("WEB_SEARCH_BASE_URL"),
		Timeout:      envDuration("WEB_SEARCH_TIMEOUT_MS", 15*time.Second),
		CacheTTL:     envDuration("WEB_SEARCH_CACHE_TTL_SECONDS", 15*time.Minute),
		CacheEntries: envInt("WEB_SEARCH_CACHE_ENTRIES", 200),
		AllowDomains: splitList(os.Getenv("WEB_SEARCH_ALLOW_DOMAINS")),
		DenyDomains:  splitList(os.Getenv("WEB_SEARCH_DENY_DOMAINS")),
	}
	client, err := New(cfg, nil)
	if err != nil {
		logging.App().Error("web_search_init_failed", "provider", cfg.Provider, "error", err)
		return nil, nil
	}
	logging.App().Info("web_search_enabled", "provider", strings.ToLower(cfg.Provider), "allow_domains", len(cfg.AllowDomains))
	return client, nil
}

// Search runs a query, applying the domain policy and cache.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	if c == nil {
		return nil, errors.New("web search is not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	key := strings.ToLower(query) + "|" + strconv.Itoa(limit)
	if cached, ok := c.cache.get(key); ok {
		return cached, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	results, err := c.provider.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	results = filterResults(results, c.cfg.AllowDomains, c.cfg.DenyDomains, limit)
	c.cache.set(key, results)
	return results, nil
}

// Provider returns the configured provider name for logging and status output.
func (c *Client) Provider() string {
	if c == nil {
		return ""
	}
	return strings.ToLower(c.cfg.Provider)
}

func filterResults(results []Result, allow, deny []string, limit int) []Result {
	out := make([]Result, 0, len(results))
	for _, item := range results {
		host := hostOf(item.URL)
		if host == "" {
			continue
		}
		if !domainAllowed(host, allow, deny) {
			continue
		}
		if item.Site == "" {
			item.Site = host
		}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

func domainAllowed(host string, allow, deny []string) bool {
	host = strings.ToLower(host)
	for _, entry := range deny {
		if matchesDomain(host, entry) {
			return false
		}
	}
	if len(allow) == 0 {
		return true
	}
	for _, entry := range allow {
		if matchesDomain(host, entry) {
			return true
		}
	}
	return false
}

func matchesDomain(host, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" {
		return false
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}

func hostOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if strings.EqualFold(parsed.Scheme, "https") || strings.EqualFold(parsed.Scheme, "http") {
		return parsed.Hostname()
	}
	return ""
}

func defaultBase(value, fallback string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return fallback
	}
	return value
}

func splitList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envFlag(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	if strings.HasSuffix(key, "_MS") {
		return time.Duration(parsed) * time.Millisecond
	}
	return time.Duration(parsed) * time.Second
}
