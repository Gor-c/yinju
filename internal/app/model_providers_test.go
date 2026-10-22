package app

import (
	"net/http"
	"testing"
)

func TestApplySelectionOverridesChosenRoleOnly(t *testing.T) {
	base := modelConfig{
		EmbedBaseURL: "https://legacy/v1",
		EmbedAPIKey:  "legacy-key",
		EmbedModel:   "legacy-embed",
		LLMBaseURL:   "https://legacy-llm/v1",
		LLMAPIKey:    "legacy-llm-key",
		LLMModel:     "legacy-chat",
	}
	chosen := []chosenProvider{
		{
			Role: modelRoleLLM,
			Provider: modelProvider{
				ID: 3, Name: "本地 Ollama", BaseURL: "http://localhost:11434/v1/",
				LLMModel: "qwen2.5:7b",
			},
		},
	}

	cfg := applySelection(base, chosen)
	if cfg.LLMBaseURL != "http://localhost:11434/v1" {
		t.Fatalf("base url should be normalized from provider: %q", cfg.LLMBaseURL)
	}
	if cfg.LLMModel != "qwen2.5:7b" {
		t.Fatalf("chat model should come from provider: %q", cfg.LLMModel)
	}
	if cfg.LLMAPIKey != "legacy-llm-key" {
		t.Fatalf("empty provider key should keep the fallback: %q", cfg.LLMAPIKey)
	}
	if cfg.EmbedBaseURL != "https://legacy/v1" || cfg.EmbedModel != "legacy-embed" {
		t.Fatalf("embedding config must stay untouched: %#v", cfg)
	}
}

func TestApplySelectionEmbedAndAPIKey(t *testing.T) {
	chosen := []chosenProvider{
		{
			Role: modelRoleEmbed,
			Provider: modelProvider{
				BaseURL: "https://cloud.example.com/v1", APIKey: "sk-provider", EmbedModel: "bge-m3",
			},
		},
	}
	cfg := applySelection(modelConfig{EmbedModel: "old", EmbedAPIKey: "old-key"}, chosen)
	if cfg.EmbedModel != "bge-m3" || cfg.EmbedAPIKey != "sk-provider" {
		t.Fatalf("embedding role should be fully overridden: %#v", cfg)
	}
}

func TestApplySelectionNoChoiceKeepsConfig(t *testing.T) {
	base := modelConfig{LLMBaseURL: "https://env/v1", LLMModel: "env-chat"}
	if got := applySelection(base, nil); got != base {
		t.Fatalf("no selection should be a no-op: %#v", got)
	}
}

func TestIsLocalBaseURL(t *testing.T) {
	local := []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:8000/v1",
		"http://192.168.1.9:11434/v1",
		"http://host.docker.internal:11434/v1",
		"http://gpu-box.local/v1",
	}
	for _, raw := range local {
		if !isLocalBaseURL(raw) {
			t.Errorf("isLocalBaseURL(%q) = false, want true", raw)
		}
	}
	remote := []string{"https://api.siliconflow.cn/v1", "https://8.8.8.8/v1", ""}
	for _, raw := range remote {
		if isLocalBaseURL(raw) {
			t.Errorf("isLocalBaseURL(%q) = true, want false", raw)
		}
	}
}

func TestMissingModelCredential(t *testing.T) {
	if missingModelCredential("http://localhost:11434/v1", "") {
		t.Fatal("local endpoint should not require an api key")
	}
	if !missingModelCredential("https://api.example.com/v1", "") {
		t.Fatal("remote endpoint should require an api key")
	}
	if missingModelCredential("https://api.example.com/v1", "sk-1") {
		t.Fatal("configured key should satisfy the requirement")
	}
}

func TestApplyProviderInputPointerSemantics(t *testing.T) {
	existing := modelProvider{Name: "old", BaseURL: "https://old/v1", APIKey: "secret", LLMModel: "m1"}
	name := "  新来源  "
	empty := ""
	applyProviderInput(&existing, providerInput{Name: &name})
	if existing.Name != "新来源" {
		t.Fatalf("name should be trimmed: %q", existing.Name)
	}
	if existing.APIKey != "secret" {
		t.Fatalf("nil api_key must keep the stored secret: %q", existing.APIKey)
	}
	applyProviderInput(&existing, providerInput{APIKey: &empty})
	if existing.APIKey != "" {
		t.Fatalf("explicit empty api_key should clear it: %q", existing.APIKey)
	}
	applyProviderInput(&existing, providerInput{BaseURL: stringPtr(" https://api.example.com/v1/embeddings ")})
	if existing.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("base url should be normalized: %q", existing.BaseURL)
	}
}

func TestModelProviderRoutesRequireDatabase(t *testing.T) {
	r := testRouter(testServer())
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/settings/models/providers"},
		{http.MethodPut, "/api/v1/settings/models/providers/1"},
		{http.MethodDelete, "/api/v1/settings/models/providers/1"},
		{http.MethodPost, "/api/v1/settings/models/providers/1/test"},
		{http.MethodPut, "/api/v1/settings/models/selection"},
	} {
		w := doRequest(r, tc.method, tc.path, `{}`, nil)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503", tc.method, tc.path, w.Code)
		}
	}
}

func stringPtr(v string) *string { return &v }
