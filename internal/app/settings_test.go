package app

import (
	"context"
	"net/http"
	"testing"
)

func TestMergeModelConfigPrecedence(t *testing.T) {
	stored := modelConfig{EmbedBaseURL: "https://db.example.com/v1/", EmbedModel: "db-embed"}
	fromEnv := modelConfig{
		EmbedBaseURL: "https://env.example.com/v1",
		EmbedAPIKey:  "env-key",
		EmbedModel:   "env-embed",
		LLMBaseURL:   "https://env-llm/v1",
		LLMModel:     "env-chat",
	}

	cfg := mergeModelConfig(stored, fromEnv)
	if cfg.EmbedBaseURL != "https://db.example.com/v1" {
		t.Fatalf("db base_url should win and be trimmed: %q", cfg.EmbedBaseURL)
	}
	if cfg.EmbedAPIKey != "env-key" {
		t.Fatalf("empty db key should fall back to env: %q", cfg.EmbedAPIKey)
	}
	if cfg.EmbedModel != "db-embed" {
		t.Fatalf("db model should win: %q", cfg.EmbedModel)
	}
	if cfg.LLMBaseURL != "https://env-llm/v1" || cfg.LLMModel != "env-chat" {
		t.Fatalf("llm should come from env: %#v", cfg)
	}

	defaults := mergeModelConfig(modelConfig{}, modelConfig{})
	if defaults.EmbedModel != defaultEmbedModel || defaults.LLMModel != defaultLLMModel {
		t.Fatalf("model names should fall back to defaults: %#v", defaults)
	}
}

func TestModelConfigCacheInvalidation(t *testing.T) {
	t.Setenv("EMBED_BASE_URL", "https://one/v1")
	t.Setenv("EMBED_MODEL", "one-embed")
	t.Setenv("LLM_BASE_URL", "https://llm/v1")
	t.Setenv("LLM_API_KEY", "key")

	s := testServer()
	first := s.modelConfigFor(context.Background())
	if first.EmbedModel != "one-embed" {
		t.Fatalf("unexpected first config: %#v", first)
	}

	t.Setenv("EMBED_MODEL", "two-embed")
	if cached := s.modelConfigFor(context.Background()); cached.EmbedModel != "one-embed" {
		t.Fatalf("cache should keep first value, got %q", cached.EmbedModel)
	}

	s.invalidateModelConfig()
	if refreshed := s.modelConfigFor(context.Background()); refreshed.EmbedModel != "two-embed" {
		t.Fatalf("invalidate should reload env, got %q", refreshed.EmbedModel)
	}
}

func TestConfigSourceAndMasking(t *testing.T) {
	cases := []struct {
		stored, fromEnv, want string
	}{
		{"db-value", "env-value", "db"},
		{"", "env-value", "env"},
		{"", "", "unset"},
	}
	for _, c := range cases {
		if got := configSource(c.stored, c.fromEnv); got != c.want {
			t.Errorf("configSource(%q,%q) = %q, want %q", c.stored, c.fromEnv, got, c.want)
		}
	}

	if got := maskKey(""); got != "" {
		t.Errorf("maskKey(empty) = %q", got)
	}
	if got := maskKey("abcd"); got != "••••" {
		t.Errorf("maskKey(short) = %q", got)
	}
	if got := maskKey("sk-1234567890abcd"); got != "••••abcd" {
		t.Errorf("maskKey = %q", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://token.sensenova.cn/v1":                  "https://token.sensenova.cn/v1",
		"https://token.sensenova.cn/v1/chat/completions": "https://token.sensenova.cn/v1",
		"https://token.sensenova.cn/v1/embeddings":       "https://token.sensenova.cn/v1",
		"https://api.siliconflow.cn/v1/":                 "https://api.siliconflow.cn/v1",
		"  https://api.siliconflow.cn/v1  ":              "https://api.siliconflow.cn/v1",
		"":                                               "",
	}
	for in, want := range cases {
		if got := normalizeBaseURL(in); got != want {
			t.Errorf("normalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateBaseURL(t *testing.T) {
	for _, ok := range []string{"", "http://127.0.0.1:8000/v1", "https://api.example.com/v1"} {
		if err := validateBaseURL(ok); err != nil {
			t.Errorf("validateBaseURL(%q) unexpected error: %v", ok, err)
		}
	}
	for _, bad := range []string{"api.example.com/v1", "ftp://example.com", "https://"} {
		if err := validateBaseURL(bad); err == nil {
			t.Errorf("validateBaseURL(%q) expected error", bad)
		}
	}
}

func TestModelSettingsRequiresDatabase(t *testing.T) {
	r := testRouter(testServer())

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/settings/models"},
		{http.MethodPut, "/api/v1/settings/models"},
		{http.MethodPost, "/api/v1/settings/models/test"},
	} {
		w := doRequest(r, tc.method, tc.path, `{}`, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503", tc.method, tc.path, w.Code)
		}
	}
}
