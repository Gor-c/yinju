package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/websearch"
)

// Web search is configured like the model settings: a single-row table wins over
// environment variables, and the key is never returned in the clear.
const webSearchConfigTTL = 60 * time.Second

type webSearchConfig struct {
	Enabled         bool
	Provider        string
	APIKey          string
	BaseURL         string
	TimeoutMS       int
	CacheTTLSeconds int
	CacheEntries    int
	AllowDomains    string
	DenyDomains     string
	AutoImport      bool
}

func envWebSearchConfig() webSearchConfig {
	return webSearchConfig{
		Enabled:         envFlag("WEB_SEARCH_ENABLED"),
		Provider:        strings.TrimSpace(env("WEB_SEARCH_PROVIDER", "")),
		APIKey:          strings.TrimSpace(env("WEB_SEARCH_API_KEY", "")),
		BaseURL:         strings.TrimSpace(env("WEB_SEARCH_BASE_URL", "")),
		TimeoutMS:       envIntValue("WEB_SEARCH_TIMEOUT_MS", 15000),
		CacheTTLSeconds: envIntValue("WEB_SEARCH_CACHE_TTL_SECONDS", 900),
		CacheEntries:    envIntValue("WEB_SEARCH_CACHE_ENTRIES", 200),
		AllowDomains:    strings.TrimSpace(env("WEB_SEARCH_ALLOW_DOMAINS", "")),
		DenyDomains:     strings.TrimSpace(env("WEB_SEARCH_DENY_DOMAINS", "")),
	}
}

func (c webSearchConfig) clientConfig() websearch.Config {
	return websearch.Config{
		Enabled:      c.Enabled,
		Provider:     c.Provider,
		APIKey:       c.APIKey,
		BaseURL:      c.BaseURL,
		Timeout:      time.Duration(c.TimeoutMS) * time.Millisecond,
		CacheTTL:     time.Duration(c.CacheTTLSeconds) * time.Second,
		CacheEntries: c.CacheEntries,
		AllowDomains: splitList(c.AllowDomains),
		DenyDomains:  splitList(c.DenyDomains),
	}
}

func (c webSearchConfig) valid() bool {
	return c.Enabled && c.Provider != "" && c.APIKey != ""
}

// webSearchConfigFor merges stored settings over environment values with the
// same cache TTL used for model settings.
func (s *Server) webSearchConfigFor(ctx context.Context) webSearchConfig {
	s.webMu.RLock()
	cached, cachedAt := s.webCfg, s.webAt
	s.webMu.RUnlock()
	if time.Since(cachedAt) < webSearchConfigTTL {
		return cached
	}
	stored, found, err := s.loadStoredWebSearchConfig(ctx)
	cfg := envWebSearchConfig()
	if found {
		cfg = stored
	}
	if err != nil {
		// Do not cache a transient database error as "disabled".
		return cfg
	}
	s.webMu.Lock()
	s.webCfg = cfg
	s.webAt = time.Now()
	s.webMu.Unlock()
	return cfg
}

func (s *Server) invalidateWebSearchConfig() {
	s.webMu.Lock()
	s.webAt = time.Time{}
	s.webClient = nil
	s.webMu.Unlock()
}

// loadStoredWebSearchConfig reports whether a row exists so an explicitly
// disabled configuration is not overridden by environment defaults.
func (s *Server) loadStoredWebSearchConfig(ctx context.Context) (webSearchConfig, bool, error) {
	if s.db == nil {
		return webSearchConfig{}, false, nil
	}
	var cfg webSearchConfig
	var updatedAt time.Time
	err := s.db.QueryRow(ctx, `SELECT enabled,provider,api_key,base_url,timeout_ms,cache_ttl_seconds,
		cache_entries,allow_domains,deny_domains,auto_import,updated_at
		FROM web_search_settings WHERE id=1`).
		Scan(&cfg.Enabled, &cfg.Provider, &cfg.APIKey, &cfg.BaseURL, &cfg.TimeoutMS,
			&cfg.CacheTTLSeconds, &cfg.CacheEntries, &cfg.AllowDomains, &cfg.DenyDomains,
			&cfg.AutoImport, &updatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return webSearchConfig{}, false, nil
		}
		logging.App().Warn("web_search_settings_read_failed", "error", err)
		return webSearchConfig{}, false, err
	}
	return cfg, true, nil
}

func (s *Server) saveWebSearchConfig(ctx context.Context, cfg webSearchConfig) error {
	if s.db == nil {
		return errDatabaseNotConfigured
	}
	_, err := s.db.Exec(ctx, `INSERT INTO web_search_settings
		(id,enabled,provider,api_key,base_url,timeout_ms,cache_ttl_seconds,cache_entries,
		 allow_domains,deny_domains,auto_import,updated_at)
		VALUES (1,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,now())
		ON CONFLICT (id) DO UPDATE SET
			enabled=EXCLUDED.enabled, provider=EXCLUDED.provider, api_key=EXCLUDED.api_key,
			base_url=EXCLUDED.base_url, timeout_ms=EXCLUDED.timeout_ms,
			cache_ttl_seconds=EXCLUDED.cache_ttl_seconds, cache_entries=EXCLUDED.cache_entries,
			allow_domains=EXCLUDED.allow_domains, deny_domains=EXCLUDED.deny_domains,
			auto_import=EXCLUDED.auto_import, updated_at=now()`,
		cfg.Enabled, cfg.Provider, cfg.APIKey, cfg.BaseURL, cfg.TimeoutMS,
		cfg.CacheTTLSeconds, cfg.CacheEntries, cfg.AllowDomains, cfg.DenyDomains, cfg.AutoImport)
	return err
}

// webSearchClient returns the active client, rebuilding it whenever the stored
// configuration changes. Tests can inject one directly through Server.web.
func (s *Server) webSearchClient(ctx context.Context) *websearch.Client {
	if s.web != nil {
		return s.web
	}
	cfg := s.webSearchConfigFor(ctx)
	s.webMu.Lock()
	defer s.webMu.Unlock()
	if s.webClient != nil && s.webCfg == cfg {
		return s.webClient
	}
	client, err := websearch.New(cfg.clientConfig(), nil)
	if err != nil {
		if cfg.Enabled {
			logging.App().Warn("web_search_client_rebuild_failed", "provider", cfg.Provider, "error", err)
		}
		s.webClient = nil
		return nil
	}
	s.webCfg = cfg
	s.webClient = client
	return client
}

type webSearchSettingsInput struct {
	Enabled         *bool   `json:"enabled"`
	Provider        *string `json:"provider"`
	APIKey          *string `json:"api_key"`
	BaseURL         *string `json:"base_url"`
	TimeoutMS       *int    `json:"timeout_ms"`
	CacheTTLSeconds *int    `json:"cache_ttl_seconds"`
	CacheEntries    *int    `json:"cache_entries"`
	AllowDomains    *string `json:"allow_domains"`
	DenyDomains     *string `json:"deny_domains"`
	AutoImport      *bool   `json:"auto_import"`
}

func (s *Server) webSearchSettings(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	cfg := s.webSearchConfigFor(c)
	_, found, _ := s.loadStoredWebSearchConfig(c)
	source := "env"
	if found {
		source = "database"
	}
	var updatedAt any
	if s.db != nil {
		var at time.Time
		if err := s.db.QueryRow(c, `SELECT updated_at FROM web_search_settings WHERE id=1`).Scan(&at); err == nil {
			updatedAt = at
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":            cfg.Enabled,
		"provider":           cfg.Provider,
		"api_key_configured": cfg.APIKey != "",
		"api_key_masked":     maskKey(cfg.APIKey),
		"base_url":           cfg.BaseURL,
		"timeout_ms":         cfg.TimeoutMS,
		"cache_ttl_seconds":  cfg.CacheTTLSeconds,
		"cache_entries":      cfg.CacheEntries,
		"allow_domains":      cfg.AllowDomains,
		"deny_domains":       cfg.DenyDomains,
		"auto_import":        cfg.AutoImport,
		"active":             s.webSearchClient(c) != nil,
		"source":             source,
		"updated_at":         updatedAt,
	})
}

func (s *Server) saveWebSearchSettings(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in webSearchSettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	cfg, found, err := s.loadStoredWebSearchConfig(c)
	if err != nil {
		fail(c, err)
		return
	}
	if !found {
		cfg = envWebSearchConfig()
	}
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	if in.Provider != nil {
		cfg.Provider = strings.TrimSpace(*in.Provider)
	}
	if in.APIKey != nil {
		cfg.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.BaseURL != nil {
		cfg.BaseURL = strings.TrimSpace(*in.BaseURL)
	}
	if in.TimeoutMS != nil {
		cfg.TimeoutMS = clampInt(*in.TimeoutMS, 1000, 60000)
	}
	if in.CacheTTLSeconds != nil {
		cfg.CacheTTLSeconds = clampInt(*in.CacheTTLSeconds, 0, 86400)
	}
	if in.CacheEntries != nil {
		cfg.CacheEntries = clampInt(*in.CacheEntries, 1, 5000)
	}
	if in.AllowDomains != nil {
		cfg.AllowDomains = strings.TrimSpace(*in.AllowDomains)
	}
	if in.DenyDomains != nil {
		cfg.DenyDomains = strings.TrimSpace(*in.DenyDomains)
	}
	if in.AutoImport != nil {
		cfg.AutoImport = *in.AutoImport
	}
	if cfg.Enabled && (cfg.Provider == "" || cfg.APIKey == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "启用联网搜索需要 provider 和 API Key"})
		return
	}
	if err := s.saveWebSearchConfig(c, cfg); err != nil {
		fail(c, err)
		return
	}
	s.invalidateWebSearchConfig()
	logging.App().Info("web_search_settings_saved",
		"request_id", c.GetString(requestIDKey),
		"enabled", cfg.Enabled,
		"provider", cfg.Provider,
		"auto_import", cfg.AutoImport,
	)
	s.webSearchSettings(c)
}

// testWebSearch runs one query so the operator can verify the configuration.
func (s *Server) testWebSearch(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in struct {
		Query string `json:"query"`
	}
	_ = c.ShouldBindJSON(&in)
	query := strings.TrimSpace(in.Query)
	if query == "" {
		query = "知识库"
	}
	client := s.webSearchClient(c)
	if client == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "联网搜索未启用或配置不完整"})
		return
	}
	results, err := client.Search(c, query, 3)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(results), "provider": client.Provider()})
}

// maskKey lives in settings.go and is shared with the model settings API.

func clampInt(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func envFlag(key string) bool {
	value := strings.TrimSpace(env(key, ""))
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}

func envIntValue(key string, fallback int) int {
	value := strings.TrimSpace(env(key, ""))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
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
