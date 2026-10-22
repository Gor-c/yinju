package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

const (
	defaultEmbedModel = "BAAI/bge-m3"
	defaultLLMModel   = "Qwen/Qwen2.5-7B-Instruct"

	// modelConfigTTL 限制配置缓存的有效期：保存设置会立即失效，
	// 直接改数据库或多实例部署也能在 TTL 内同步。
	modelConfigTTL = 60 * time.Second
)

// modelConfig 是模型服务的运行期配置。数据库 model_settings 中的非空值优先，
// 未配置的字段回落到环境变量，模型名最终回落到内置默认值。
type modelConfig struct {
	EmbedBaseURL string
	EmbedAPIKey  string
	EmbedModel   string
	LLMBaseURL   string
	LLMAPIKey    string
	LLMModel     string
}

func envModelConfig() modelConfig {
	return modelConfig{
		EmbedBaseURL: env("EMBED_BASE_URL", ""),
		EmbedAPIKey:  env("EMBED_API_KEY", ""),
		EmbedModel:   env("EMBED_MODEL", ""),
		LLMBaseURL:   env("LLM_BASE_URL", ""),
		LLMAPIKey:    env("LLM_API_KEY", ""),
		LLMModel:     env("LLM_MODEL", ""),
	}
}

// mergeModelConfig 逐字段合并：stored（数据库）非空优先，其次 fallback（环境变量），
// 模型名最后回落到默认值。
func mergeModelConfig(stored, fallback modelConfig) modelConfig {
	pick := func(values ...string) string {
		for _, v := range values {
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	return modelConfig{
		EmbedBaseURL: normalizeBaseURL(pick(stored.EmbedBaseURL, fallback.EmbedBaseURL)),
		EmbedAPIKey:  pick(stored.EmbedAPIKey, fallback.EmbedAPIKey),
		EmbedModel:   pick(stored.EmbedModel, fallback.EmbedModel, defaultEmbedModel),
		LLMBaseURL:   normalizeBaseURL(pick(stored.LLMBaseURL, fallback.LLMBaseURL)),
		LLMAPIKey:    pick(stored.LLMAPIKey, fallback.LLMAPIKey),
		LLMModel:     pick(stored.LLMModel, fallback.LLMModel, defaultLLMModel),
	}
}

// modelConfigFor 返回当前生效的模型配置。缓存带 TTL，保存设置时立即失效，
// 这样直接改数据库或多实例部署也能在 modelConfigTTL 内生效。
func (s *Server) modelConfigFor(ctx context.Context) modelConfig {
	s.modelsMu.RLock()
	cached, cachedAt := s.models, s.modelsAt
	s.modelsMu.RUnlock()
	if cached != nil && time.Since(cachedAt) < modelConfigTTL {
		return *cached
	}

	stored, err := s.loadStoredModelConfig(ctx)
	cfg := mergeModelConfig(stored, envModelConfig())
	if err != nil {
		// 读取失败时不缓存：否则一次瞬时数据库错误会把「未配置」永久留在缓存里。
		return cfg
	}
	// 选中的模型来源优先于手工配置：一个来源可同时提供对话与向量，
	// 也可拆成两个来源分别承接，未配置的字段仍沿用下面的回落链。
	chosen, selErr := s.loadChosenProviders(ctx)
	cfg = applySelection(cfg, chosen)
	if selErr != nil {
		logging.App().Warn("model_selection_read_failed", "error", selErr)
		return cfg
	}

	s.modelsMu.Lock()
	s.models = &cfg
	s.modelsAt = time.Now()
	s.modelsMu.Unlock()
	return cfg
}

func (s *Server) invalidateModelConfig() {
	s.modelsMu.Lock()
	s.models = nil
	s.modelsAt = time.Time{}
	s.modelsMu.Unlock()
}

// storedModelConfig 读取 model_settings 单行记录；读取失败时返回空配置，
// 由环境变量兜底，不影响服务启动。
func (s *Server) storedModelConfig(ctx context.Context) modelConfig {
	cfg, _ := s.loadStoredModelConfig(ctx)
	return cfg
}

// loadStoredModelConfig 区分「没有记录/没有数据库」和「读取失败」：
// 前者返回空配置 + nil，后者返回 error，供调用方决定是否缓存。
func (s *Server) loadStoredModelConfig(ctx context.Context) (modelConfig, error) {
	if s.db == nil {
		return modelConfig{}, nil
	}
	var cfg modelConfig
	err := s.db.QueryRow(ctx, `SELECT embed_base_url,embed_api_key,embed_model,llm_base_url,llm_api_key,llm_model
		FROM model_settings WHERE id=1`).
		Scan(&cfg.EmbedBaseURL, &cfg.EmbedAPIKey, &cfg.EmbedModel, &cfg.LLMBaseURL, &cfg.LLMAPIKey, &cfg.LLMModel)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return modelConfig{}, nil
		}
		logging.App().Warn("model_settings_read_failed", "error", err)
		return modelConfig{}, err
	}
	return cfg, nil
}

func (s *Server) saveModelConfig(ctx context.Context, cfg modelConfig) error {
	if s.db == nil {
		return errDatabaseNotConfigured
	}
	_, err := s.db.Exec(ctx, `INSERT INTO model_settings
		(id,embed_base_url,embed_api_key,embed_model,llm_base_url,llm_api_key,llm_model,updated_at)
		VALUES (1,$1,$2,$3,$4,$5,$6,now())
		ON CONFLICT (id) DO UPDATE SET
			embed_base_url=EXCLUDED.embed_base_url, embed_api_key=EXCLUDED.embed_api_key,
			embed_model=EXCLUDED.embed_model, llm_base_url=EXCLUDED.llm_base_url,
			llm_api_key=EXCLUDED.llm_api_key, llm_model=EXCLUDED.llm_model, updated_at=now()`,
		cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel, cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	return err
}

// modelSettingsInput 是保存请求体。api_key 使用指针区分三种语义：
// 字段缺失（nil）保留原值，空字符串表示清除，非空表示覆盖。
type modelSettingsInput struct {
	EmbedBaseURL *string `json:"embed_base_url"`
	EmbedAPIKey  *string `json:"embed_api_key"`
	EmbedModel   *string `json:"embed_model"`
	LLMBaseURL   *string `json:"llm_base_url"`
	LLMAPIKey    *string `json:"llm_api_key"`
	LLMModel     *string `json:"llm_model"`
}

func (s *Server) modelSettings(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	stored := s.storedModelConfig(c)
	cfg := s.modelConfigFor(c)
	envCfg := envModelConfig()
	chosen, err := s.loadChosenProviders(c)
	if err != nil {
		logging.App().Warn("model_selection_read_failed", "error", err)
	}
	providers, err := s.listModelProviders(c)
	if err != nil {
		logging.App().Warn("model_providers_read_failed", "error", err)
	}
	llmActive, _ := selectionFor(chosen, modelRoleLLM)
	embedActive, _ := selectionFor(chosen, modelRoleEmbed)

	items := make([]gin.H, 0, len(providers))
	for _, p := range providers {
		items = append(items, providerJSON(p, p.ID == llmActive.ID, p.ID == embedActive.ID))
	}

	// 生效值仍以 embed/llm 两段返回，前端无需区分「来自来源」还是「手工配置」。
	// 来源生效时 base_url_from 记为 provider，并带上来源名便于展示。
	embedSource, llmSource := "unset", "unset"
	if embedActive.ID > 0 {
		embedSource = "provider"
	}
	if llmActive.ID > 0 {
		llmSource = "provider"
	}
	c.JSON(http.StatusOK, gin.H{
		"embed": gin.H{
			"base_url":      cfg.EmbedBaseURL,
			"model":         cfg.EmbedModel,
			"api_key_set":   cfg.EmbedAPIKey != "",
			"api_key_hint":  maskKey(cfg.EmbedAPIKey),
			"base_url_from": orSource(embedSource, configSource(stored.EmbedBaseURL, envCfg.EmbedBaseURL)),
			"api_key_from":  orSource(embedSource, configSource(stored.EmbedAPIKey, envCfg.EmbedAPIKey)),
			"model_from":    orSource(embedSource, configSource(stored.EmbedModel, envCfg.EmbedModel)),
			"provider_id":   embedActive.ID,
			"provider_name": embedActive.Name,
		},
		"llm": gin.H{
			"base_url":      cfg.LLMBaseURL,
			"model":         cfg.LLMModel,
			"api_key_set":   cfg.LLMAPIKey != "",
			"api_key_hint":  maskKey(cfg.LLMAPIKey),
			"base_url_from": orSource(llmSource, configSource(stored.LLMBaseURL, envCfg.LLMBaseURL)),
			"api_key_from":  orSource(llmSource, configSource(stored.LLMAPIKey, envCfg.LLMAPIKey)),
			"model_from":    orSource(llmSource, configSource(stored.LLMModel, envCfg.LLMModel)),
			"provider_id":   llmActive.ID,
			"provider_name": llmActive.Name,
		},
		"providers": items,
		"selection": gin.H{"llm": llmActive.ID, "embed": embedActive.ID},
		// manual 返回手工配置原值：来源生效时 embed/llm 展示的是合并后的生效值，
		// 前端编辑手工配置必须拿到未被来源覆盖的原始值，否则保存会把来源值写入库。
		"manual": gin.H{
			"embed": gin.H{"base_url": stored.EmbedBaseURL, "model": stored.EmbedModel},
			"llm":   gin.H{"base_url": stored.LLMBaseURL, "model": stored.LLMModel},
		},
		"embedding_dimensions": embeddingDimensions,
		"updated_at":           s.modelSettingsUpdatedAt(c),
	})
}

// orSource 在来源生效时把来源标记为配置出处，否则保留手工/环境变量的出处。
func orSource(providerRole string, fallback string) string {
	if providerRole == "provider" {
		return "provider"
	}
	return fallback
}

func (s *Server) saveModelSettings(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in modelSettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}

	cfg := s.storedModelConfig(c)
	if in.EmbedBaseURL != nil {
		cfg.EmbedBaseURL = normalizeBaseURL(*in.EmbedBaseURL)
	}
	if in.EmbedModel != nil {
		cfg.EmbedModel = strings.TrimSpace(*in.EmbedModel)
	}
	if in.EmbedAPIKey != nil {
		cfg.EmbedAPIKey = strings.TrimSpace(*in.EmbedAPIKey)
	}
	if in.LLMBaseURL != nil {
		cfg.LLMBaseURL = normalizeBaseURL(*in.LLMBaseURL)
	}
	if in.LLMModel != nil {
		cfg.LLMModel = strings.TrimSpace(*in.LLMModel)
	}
	if in.LLMAPIKey != nil {
		cfg.LLMAPIKey = strings.TrimSpace(*in.LLMAPIKey)
	}

	if err := validateBaseURL(cfg.EmbedBaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "embedding base_url 无效：" + err.Error()})
		return
	}
	if err := validateBaseURL(cfg.LLMBaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "chat base_url 无效：" + err.Error()})
		return
	}

	if err := s.saveModelConfig(c, cfg); err != nil {
		fail(c, err)
		return
	}
	s.invalidateModelConfig()
	logging.App().Info("model_settings_saved",
		"request_id", c.GetString(requestIDKey),
		"embed_base_url", cfg.EmbedBaseURL,
		"embed_model", cfg.EmbedModel,
		"llm_base_url", cfg.LLMBaseURL,
		"llm_model", cfg.LLMModel,
	)
	s.modelSettings(c)
}

type modelListInput struct {
	Kind       string `json:"kind"`
	BaseURL    string `json:"base_url"`
	APIKey     string `json:"api_key"`
	ProviderID int64  `json:"provider_id"`
}

type availableModel struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	ContextLength     int      `json:"context_length"`
	SupportedFeatures []string `json:"supported_features"`
	InputModalities   []string `json:"input_modalities"`
	OutputModalities  []string `json:"output_modalities"`
}

// listAvailableModels 代理 OpenAI-compatible 的 GET {base_url}/models，
// 允许前端在填写地址和 key 后立即拉取候选模型。请求体中的 base_url/api_key
// 优先于已保存配置，便于「先填后选」再保存。
func (s *Server) listAvailableModels(c *gin.Context) {
	var in modelListInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind != "embed" && kind != "llm" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind 必须是 embed 或 llm"})
		return
	}

	cfg := s.modelConfigFor(c)
	base, key := cfg.EmbedBaseURL, cfg.EmbedAPIKey
	if kind == "llm" {
		base, key = cfg.LLMBaseURL, cfg.LLMAPIKey
	}
	// 已保存的来源优先，便于「编辑来源时重新拉列表」而不必重填 Key。
	if in.ProviderID > 0 {
		if p, err := s.getModelProvider(c, in.ProviderID); err == nil {
			if p.BaseURL != "" {
				base = normalizeBaseURL(p.BaseURL)
			}
			if p.APIKey != "" {
				key = p.APIKey
			}
		}
	}
	if v := normalizeBaseURL(in.BaseURL); v != "" {
		base = v
	}
	if v := strings.TrimSpace(in.APIKey); v != "" {
		key = v
	}
	if base == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先填写 Base URL"})
		return
	}
	if err := validateBaseURL(base); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL 无效：" + err.Error()})
		return
	}
	if missingModelCredential(base, key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先填写 API Key 再获取模型列表"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL 无效：" + err.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	res, err := s.http.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "请求模型列表失败：" + err.Error()})
		return
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{
			"error": fmt.Sprintf("上游返回 %d：%s", res.StatusCode, truncateRunes(strings.TrimSpace(string(raw)), 300)),
		})
		return
	}

	var parsed struct {
		Data []availableModel `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "返回内容不是 OpenAI 兼容的模型列表"})
		return
	}
	out := make([]availableModel, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		if strings.TrimSpace(m.Name) == "" {
			m.Name = m.ID
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	logging.App().Info("model_list_fetched", "request_id", c.GetString(requestIDKey), "kind", kind, "count", len(out))
	c.JSON(http.StatusOK, gin.H{"kind": kind, "count": len(out), "models": out})
}

type modelTestInput struct {
	Kind string `json:"kind"`
}

// testModelSettings 用当前生效配置发最小请求。kind 为 embed/llm 时只测对应服务，
// 省略（或请求体为空）时两个都测；单项失败不影响另一项，HTTP 状态统一为 200。
func (s *Server) testModelSettings(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in modelTestInput
	_ = c.ShouldBindJSON(&in)
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind != "" && kind != "embed" && kind != "llm" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind 必须是 embed 或 llm"})
		return
	}

	ctx, cancel := context.WithTimeout(c, 30*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, s.runModelTest(ctx, s.modelConfigFor(c), kind))
}

// runModelTest 用给定配置发最小请求。kind 为空时两项都测；单项失败不影响另一项，
// HTTP 状态统一 200，由结果里的 ok 表达。来源测试与生效配置测试共用这段逻辑。
func (s *Server) runModelTest(ctx context.Context, cfg modelConfig, kind string) gin.H {
	testEmbed := kind == "" || kind == modelRoleEmbed
	testChat := kind == "" || kind == modelRoleLLM

	out := gin.H{}
	if testEmbed {
		embedStart := time.Now()
		vecs, embedErr := s.embeddingRequestWith(ctx, cfg, "/embeddings", gin.H{"model": cfg.EmbedModel, "input": []string{"连接测试"}})
		embedResult := gin.H{"ok": embedErr == nil, "latency_ms": time.Since(embedStart).Milliseconds()}
		if embedErr != nil {
			embedResult["error"] = embedErr.Error()
		} else if len(vecs) > 0 {
			embedResult["dimensions"] = len(vecs[0])
			if len(vecs[0]) != embeddingDimensions {
				embedResult["ok"] = false
				embedResult["error"] = fmt.Sprintf("返回 %d 维，期望 %d 维；直接入库会导致索引不一致", len(vecs[0]), embeddingDimensions)
			}
		}
		out["embedding"] = embedResult
	}
	if testChat {
		chatStart := time.Now()
		_, chatErr := s.completeMessagesWith(ctx, cfg, []gin.H{{"role": "user", "content": "ping"}})
		chatResult := gin.H{"ok": chatErr == nil, "latency_ms": time.Since(chatStart).Milliseconds()}
		if chatErr != nil {
			chatResult["error"] = chatErr.Error()
		}
		out["chat"] = chatResult
	}
	return out
}

func (s *Server) modelSettingsUpdatedAt(ctx context.Context) any {
	if s.db == nil {
		return nil
	}
	var at *time.Time
	if err := s.db.QueryRow(ctx, `SELECT updated_at FROM model_settings WHERE id=1`).Scan(&at); err != nil {
		return nil
	}
	return at
}

// normalizeBaseURL 允许直接粘贴完整 endpoint：去掉已知路径后缀和尾部斜杠，
// 保存与读取时都按「/v1 这一层」处理，避免拼出重复路径。
func normalizeBaseURL(raw string) string {
	v := strings.TrimSpace(raw)
	for _, suffix := range []string{"/chat/completions", "/completions", "/embeddings", "/embeddings/create"} {
		if strings.HasSuffix(v, suffix) {
			v = strings.TrimSuffix(v, suffix)
			break
		}
	}
	return strings.TrimRight(v, "/")
}

func validateBaseURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("必须以 http:// 或 https:// 开头")
	}
	if u.Host == "" {
		return errors.New("缺少主机名")
	}
	return nil
}

// configSource 标记某个字段的来源，供前端提示配置被谁覆盖。
func configSource(stored, fromEnv string) string {
	switch {
	case strings.TrimSpace(stored) != "":
		return "db"
	case strings.TrimSpace(fromEnv) != "":
		return "env"
	default:
		return "unset"
	}
}

func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}
