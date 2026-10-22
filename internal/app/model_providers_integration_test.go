package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 连真实数据库验证来源的增删改查与切换链路：
//
//	PG_INTEGRATION_TEST=1 PG_DSN=... go test ./internal/app -run TestModelProviderIntegration -v
//
// 测试结束会删除自己创建的来源并恢复原选择，不会污染现有配置。
func TestModelProviderIntegration(t *testing.T) {
	if os.Getenv("PG_INTEGRATION_TEST") != "1" {
		t.Skip("set PG_INTEGRATION_TEST=1 and PG_DSN to use a live database")
	}
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	s := &Server{db: pool, http: &http.Client{Timeout: 10 * time.Second}}
	s.ensureModelProviderSchema(ctx)

	var originalLLM int64
	_ = pool.QueryRow(ctx, `SELECT provider_id FROM model_selection WHERE role='llm'`).Scan(&originalLLM)

	r := testRouter(s)
	created := doRequest(r, http.MethodPost, "/api/v1/settings/models/providers",
		`{"name":"集成测试来源","base_url":"http://localhost:11434/v1/","api_key":"","llm_model":"qwen2.5:7b","embed_model":"bge-m3"}`, nil)
	if created.Code != http.StatusOK {
		t.Fatalf("create provider status=%d body=%s", created.Code, created.Body.String())
	}
	var createResp struct {
		Provider struct {
			ID int64 `json:"id"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id := createResp.Provider.ID
	if id == 0 {
		t.Fatal("provider id is empty")
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM model_selection WHERE provider_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM model_providers WHERE id=$1`, id)
		if originalLLM > 0 {
			_, _ = pool.Exec(context.Background(), `INSERT INTO model_selection (role,provider_id) VALUES ('llm',$1)
				ON CONFLICT (role) DO UPDATE SET provider_id=EXCLUDED.provider_id`, originalLLM)
		}
	}()

	// 切换为对话来源后，生效配置应立即指向该来源（缓存已失效）。
	switched := doRequest(r, http.MethodPut, "/api/v1/settings/models/selection",
		`{"role":"llm","provider_id":`+strconv.FormatInt(id, 10)+`}`, nil)
	if switched.Code != http.StatusOK {
		t.Fatalf("switch selection status=%d body=%s", switched.Code, switched.Body.String())
	}

	settings := doRequest(r, http.MethodGet, "/api/v1/settings/models", "", nil)
	if settings.Code != http.StatusOK {
		t.Fatalf("get settings status=%d", settings.Code)
	}
	var meta struct {
		LLM struct {
			BaseURL      string `json:"base_url"`
			Model        string `json:"model"`
			ProviderName string `json:"provider_name"`
		} `json:"llm"`
		Embed struct {
			ProviderName string `json:"provider_name"`
		} `json:"embed"`
		Selection struct {
			LLM int64 `json:"llm"`
		} `json:"selection"`
	}
	if err := json.Unmarshal(settings.Body.Bytes(), &meta); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if meta.Selection.LLM != id || meta.LLM.ProviderName != "集成测试来源" {
		t.Fatalf("selection not applied: %#v", meta)
	}
	if meta.LLM.BaseURL != "http://localhost:11434/v1" || meta.LLM.Model != "qwen2.5:7b" {
		t.Fatalf("effective chat config should come from provider: %#v", meta.LLM)
	}
	if meta.Embed.ProviderName != "" {
		t.Fatalf("embedding should not be switched: %#v", meta.Embed)
	}

	// 只有对话模型的来源不能被选为向量来源。
	onlyChat := doRequest(r, http.MethodPost, "/api/v1/settings/models/providers",
		`{"name":"仅对话来源","base_url":"http://localhost:11434/v1","llm_model":"qwen2.5:7b"}`, nil)
	if onlyChat.Code != http.StatusOK {
		t.Fatalf("create chat-only provider status=%d body=%s", onlyChat.Code, onlyChat.Body.String())
	}
	var chatResp struct {
		Provider struct {
			ID int64 `json:"id"`
		} `json:"provider"`
	}
	_ = json.Unmarshal(onlyChat.Body.Bytes(), &chatResp)
	chatID := chatResp.Provider.ID
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM model_selection WHERE provider_id=$1`, chatID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM model_providers WHERE id=$1`, chatID)
	}()
	badEmbed := doRequest(r, http.MethodPut, "/api/v1/settings/models/selection",
		`{"role":"embed","provider_id":`+strconv.FormatInt(chatID, 10)+`}`, nil)
	if badEmbed.Code != http.StatusBadRequest {
		t.Fatalf("chat-only provider must be rejected for embed role, got %d body=%s", badEmbed.Code, badEmbed.Body.String())
	}

	deleted := doRequest(r, http.MethodDelete, "/api/v1/settings/models/providers/"+strconv.FormatInt(id, 10), "", nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete provider status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	var remaining int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM model_selection WHERE provider_id=$1`, id).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("selection rows should be cleared with the provider, got %d", remaining)
	}
}
