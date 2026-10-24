package app

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

// 模型来源（provider）是「一个 OpenAI 兼容端点 + 一组模型名」的可保存配置。
// 对话与向量可以各自指向不同来源（例如本地 Ollama 对话 + 云端向量），
// 当前选择记录在 model_selection；未选择时回落到 model_settings 手工配置，
// 再回落到 EMBED_* / LLM_* 环境变量，保证旧部署行为不变。
type modelProvider struct {
	ID          int64
	Name        string
	BaseURL     string
	APIKey      string
	LLMModel    string
	EmbedModel  string
	RerankModel string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const (
	modelRoleLLM    = "llm"
	modelRoleEmbed  = "embed"
	modelRoleRerank = "rerank"
)

// chosenProvider 表示某个角色当前选中的来源。
type chosenProvider struct {
	Role     string
	Provider modelProvider
}

const createModelProvidersTable = `CREATE TABLE IF NOT EXISTS model_providers (
	id bigserial PRIMARY KEY,
	name text NOT NULL DEFAULT '',
	base_url text NOT NULL DEFAULT '',
	api_key text NOT NULL DEFAULT '',
	llm_model text NOT NULL DEFAULT '',
	embed_model text NOT NULL DEFAULT '',
	rerank_model text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
)`

// model_selection 只有两行（llm/embed），provider_id = 0 表示不指定来源。
const createModelSelectionTable = `CREATE TABLE IF NOT EXISTS model_selection (
	role text PRIMARY KEY,
	provider_id bigint NOT NULL DEFAULT 0,
	updated_at timestamptz NOT NULL DEFAULT now(),
	CONSTRAINT model_selection_role_check CHECK (role IN ('llm','embed','rerank'))
)`

// ensureModelProviderSchema 让老库无需手工迁移即可使用多来源配置。
// 无 DDL 权限时只记录告警，服务继续用旧的单来源配置运行。
func (s *Server) ensureModelProviderSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	for _, stmt := range []string{createModelProvidersTable, createModelSelectionTable} {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("model_provider_schema_failed", "error", err)
			return
		}
	}
	// 老库补 rerank_model 列，并把角色约束放宽到 rerank。
	migrations := []string{
		`ALTER TABLE model_providers ADD COLUMN IF NOT EXISTS rerank_model text NOT NULL DEFAULT ''`,
		`ALTER TABLE model_selection DROP CONSTRAINT IF EXISTS model_selection_role_check`,
		`ALTER TABLE model_selection ADD CONSTRAINT model_selection_role_check CHECK (role IN ('llm','embed','rerank'))`,
	}
	for _, stmt := range migrations {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("model_provider_migration_failed", "error", err)
			return
		}
	}
}

func scanModelProvider(row pgx.Row) (modelProvider, error) {
	var p modelProvider
	err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.APIKey, &p.LLMModel, &p.EmbedModel, &p.RerankModel, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

const modelProviderColumns = `id,name,base_url,api_key,llm_model,embed_model,rerank_model,created_at,updated_at`

func (s *Server) listModelProviders(ctx context.Context) ([]modelProvider, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+modelProviderColumns+` FROM model_providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]modelProvider, 0, 4)
	for rows.Next() {
		p, scanErr := scanModelProvider(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Server) getModelProvider(ctx context.Context, id int64) (modelProvider, error) {
	if s.db == nil {
		return modelProvider{}, errDatabaseNotConfigured
	}
	return scanModelProvider(s.db.QueryRow(ctx, `SELECT `+modelProviderColumns+` FROM model_providers WHERE id=$1`, id))
}

// loadChosenProviders 一次 JOIN 取回当前选中的来源；没有选择时返回空切片。
func (s *Server) loadChosenProviders(ctx context.Context) ([]chosenProvider, error) {
	if s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT ms.role, p.id, p.name, p.base_url, p.api_key, p.llm_model, p.embed_model, p.rerank_model, p.created_at, p.updated_at
		FROM model_selection ms
		JOIN model_providers p ON p.id = ms.provider_id
		WHERE ms.provider_id > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]chosenProvider, 0, 2)
	for rows.Next() {
		var item chosenProvider
		if err := rows.Scan(&item.Role, &item.Provider.ID, &item.Provider.Name, &item.Provider.BaseURL,
			&item.Provider.APIKey, &item.Provider.LLMModel, &item.Provider.EmbedModel, &item.Provider.RerankModel,
			&item.Provider.CreatedAt, &item.Provider.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// applySelection 把选中的来源覆盖到手工/环境变量配置之上。
// 来源里为空的字段不覆盖，避免一个只填了对话模型的来源把向量配置清空。
func applySelection(cfg modelConfig, chosen []chosenProvider) modelConfig {
	for _, item := range chosen {
		p := item.Provider
		switch item.Role {
		case modelRoleLLM:
			if p.BaseURL != "" {
				cfg.LLMBaseURL = normalizeBaseURL(p.BaseURL)
			}
			if p.LLMModel != "" {
				cfg.LLMModel = p.LLMModel
			}
			if p.APIKey != "" {
				cfg.LLMAPIKey = p.APIKey
			}
		case modelRoleEmbed:
			if p.BaseURL != "" {
				cfg.EmbedBaseURL = normalizeBaseURL(p.BaseURL)
			}
			if p.EmbedModel != "" {
				cfg.EmbedModel = p.EmbedModel
			}
			if p.APIKey != "" {
				cfg.EmbedAPIKey = p.APIKey
			}
		case modelRoleRerank:
			if p.BaseURL != "" {
				cfg.RerankBaseURL = normalizeBaseURL(p.BaseURL)
			}
			if p.RerankModel != "" {
				cfg.RerankModel = p.RerankModel
			}
			if p.APIKey != "" {
				cfg.RerankAPIKey = p.APIKey
			}
		}
	}
	return cfg
}

func (s *Server) setModelSelection(ctx context.Context, role string, providerID int64) error {
	if s.db == nil {
		return errDatabaseNotConfigured
	}
	if providerID <= 0 {
		_, err := s.db.Exec(ctx, `DELETE FROM model_selection WHERE role=$1`, role)
		return err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO model_selection (role,provider_id,updated_at) VALUES ($1,$2,now())
		ON CONFLICT (role) DO UPDATE SET provider_id=EXCLUDED.provider_id, updated_at=now()`, role, providerID)
	return err
}

// providerJSON 输出给前端：api_key 只回显掩码，从不返回明文。
func providerJSON(p modelProvider, activeLLM, activeEmbed, activeRerank bool) gin.H {
	return gin.H{
		"id":             p.ID,
		"name":           p.Name,
		"base_url":       p.BaseURL,
		"llm_model":      p.LLMModel,
		"embed_model":    p.EmbedModel,
		"rerank_model":   p.RerankModel,
		"api_key_set":    p.APIKey != "",
		"api_key_hint":   maskKey(p.APIKey),
		"active_llm":     activeLLM,
		"active_embed":   activeEmbed,
		"active_rerank":  activeRerank,
		"updated_at":     p.UpdatedAt,
		"local_endpoint": isLocalBaseURL(p.BaseURL),
	}
}

type providerInput struct {
	Name        *string `json:"name"`
	BaseURL     *string `json:"base_url"`
	APIKey      *string `json:"api_key"`
	LLMModel    *string `json:"llm_model"`
	EmbedModel  *string `json:"embed_model"`
	RerankModel *string `json:"rerank_model"`
}

// applyProviderInput 逐字段覆盖；api_key 用指针区分「不修改」与「清除」。
func applyProviderInput(p *modelProvider, in providerInput) {
	if in.Name != nil {
		p.Name = strings.TrimSpace(*in.Name)
	}
	if in.BaseURL != nil {
		p.BaseURL = normalizeBaseURL(*in.BaseURL)
	}
	if in.APIKey != nil {
		p.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.LLMModel != nil {
		p.LLMModel = strings.TrimSpace(*in.LLMModel)
	}
	if in.EmbedModel != nil {
		p.EmbedModel = strings.TrimSpace(*in.EmbedModel)
	}
	if in.RerankModel != nil {
		p.RerankModel = strings.TrimSpace(*in.RerankModel)
	}
}

func (s *Server) createModelProvider(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in providerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	var p modelProvider
	applyProviderInput(&p, in)
	if p.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写 Base URL"})
		return
	}
	if err := validateBaseURL(p.BaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL 无效：" + err.Error()})
		return
	}
	if p.Name == "" {
		p.Name = p.BaseURL
	}
	if p.LLMModel == "" && p.EmbedModel == "" && p.RerankModel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少填写一个对话、向量或重排模型"})
		return
	}

	err := s.db.QueryRow(c, `INSERT INTO model_providers (name,base_url,api_key,llm_model,embed_model,rerank_model)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		p.Name, p.BaseURL, p.APIKey, p.LLMModel, p.EmbedModel, p.RerankModel).Scan(&p.ID)
	if err != nil {
		fail(c, err)
		return
	}
	s.invalidateModelConfig()
	logging.App().Info("model_provider_created", "request_id", c.GetString(requestIDKey), "id", p.ID, "name", p.Name)
	c.JSON(http.StatusOK, gin.H{"provider": providerJSON(p, false, false, false)})
}

func (s *Server) updateModelProvider(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	var in providerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	p, err := s.getModelProvider(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	applyProviderInput(&p, in)
	if p.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写 Base URL"})
		return
	}
	if err := validateBaseURL(p.BaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL 无效：" + err.Error()})
		return
	}
	if p.LLMModel == "" && p.EmbedModel == "" && p.RerankModel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "至少填写一个对话、向量或重排模型"})
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = p.BaseURL
	}

	_, err = s.db.Exec(c, `UPDATE model_providers SET name=$2,base_url=$3,api_key=$4,llm_model=$5,embed_model=$6,
		rerank_model=$7,updated_at=now() WHERE id=$1`,
		p.ID, p.Name, p.BaseURL, p.APIKey, p.LLMModel, p.EmbedModel, p.RerankModel)
	if err != nil {
		fail(c, err)
		return
	}
	s.invalidateModelConfig()
	logging.App().Info("model_provider_updated", "request_id", c.GetString(requestIDKey), "id", p.ID, "name", p.Name)
	c.JSON(http.StatusOK, gin.H{"provider": providerJSON(p, false, false, false)})
}

func (s *Server) deleteModelProvider(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM model_selection WHERE provider_id=$1`, id); err != nil {
		fail(c, err)
		return
	}
	tag, err := s.db.Exec(c, `DELETE FROM model_providers WHERE id=$1`, id)
	if err != nil {
		fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "来源不存在"})
		return
	}
	s.invalidateModelConfig()
	logging.App().Info("model_provider_deleted", "request_id", c.GetString(requestIDKey), "id", id)
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

type modelSelectionInput struct {
	Role       string `json:"role"`
	ProviderID int64  `json:"provider_id"`
}

// saveModelSelection 切换某个角色使用的来源；provider_id=0 表示取消，回落到手工配置。
func (s *Server) saveModelSelection(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in modelSelectionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	if role != modelRoleLLM && role != modelRoleEmbed && role != modelRoleRerank {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role 必须是 llm 或 embed"})
		return
	}
	if in.ProviderID > 0 {
		p, err := s.getModelProvider(c, in.ProviderID)
		if err != nil {
			fail(c, err)
			return
		}
		if role == modelRoleLLM && p.LLMModel == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该来源未配置对话模型，无法作为对话来源"})
			return
		}
		if role == modelRoleEmbed && p.EmbedModel == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该来源未配置向量模型，无法作为向量来源"})
			return
		}
		if p.BaseURL == "" || validateBaseURL(p.BaseURL) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "该来源的 Base URL 无效"})
			return
		}
	}
	if err := s.setModelSelection(c, role, in.ProviderID); err != nil {
		fail(c, err)
		return
	}
	s.invalidateModelConfig()
	cfg := s.modelConfigFor(c)
	logging.App().Info("model_selection_changed", "role", role, "provider_id", in.ProviderID,
		"request_id", c.GetString(requestIDKey))
	c.JSON(http.StatusOK, gin.H{
		"role":        role,
		"provider_id": in.ProviderID,
		"effective": gin.H{
			"base_url": baseURLForRole(cfg, role),
			"model":    modelForRole(cfg, role),
		},
	})
}

func baseURLForRole(cfg modelConfig, role string) string {
	if role == modelRoleLLM {
		return cfg.LLMBaseURL
	}
	return cfg.EmbedBaseURL
}

func modelForRole(cfg modelConfig, role string) string {
	if role == modelRoleLLM {
		return cfg.LLMModel
	}
	return cfg.EmbedModel
}

// providerTestInput 允许前端在保存前先测试：带上 base_url/api_key/model 即用临时值，
// 否则按 id 读取已保存的来源。
type providerTestInput struct {
	Kind       string  `json:"kind"`
	BaseURL    *string `json:"base_url"`
	APIKey     *string `json:"api_key"`
	LLMModel   *string `json:"llm_model"`
	EmbedModel *string `json:"embed_model"`
}

func (s *Server) testModelProvider(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	var in providerTestInput
	_ = c.ShouldBindJSON(&in)
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if kind != "" && kind != modelRoleLLM && kind != modelRoleEmbed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind 必须是 embed 或 llm"})
		return
	}

	p := modelProvider{}
	if id > 0 {
		p, err = s.getModelProvider(c, id)
		if err != nil {
			fail(c, err)
			return
		}
	}
	if in.BaseURL != nil {
		p.BaseURL = normalizeBaseURL(*in.BaseURL)
	}
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		p.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.LLMModel != nil {
		p.LLMModel = strings.TrimSpace(*in.LLMModel)
	}
	if in.EmbedModel != nil {
		p.EmbedModel = strings.TrimSpace(*in.EmbedModel)
	}
	if p.BaseURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先填写 Base URL"})
		return
	}
	if err := validateBaseURL(p.BaseURL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Base URL 无效：" + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c, 60*time.Second)
	defer cancel()
	base := modelConfig{EmbedBaseURL: p.BaseURL, EmbedAPIKey: p.APIKey, EmbedModel: p.EmbedModel,
		LLMBaseURL: p.BaseURL, LLMAPIKey: p.APIKey, LLMModel: p.LLMModel}
	c.JSON(http.StatusOK, s.runModelTest(ctx, base, kind))
}

// isLocalBaseURL 判断端点是否在本机/内网。本地模型（Ollama、LM Studio、vLLM）
// 通常不校验 API Key，因此这些地址允许 Key 留空。
func isLocalBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	if host == "localhost" || host == "host.docker.internal" || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	return false
}

// missingModelCredential 判断是否缺少必要的 API Key：本地端点豁免。
func missingModelCredential(base, key string) bool {
	return strings.TrimSpace(key) == "" && !isLocalBaseURL(base)
}

// selectionFor 返回角色对应的选中来源（未选中时 ok=false）。
func selectionFor(chosen []chosenProvider, role string) (modelProvider, bool) {
	for _, item := range chosen {
		if item.Role == role {
			return item.Provider, true
		}
	}
	return modelProvider{}, false
}
