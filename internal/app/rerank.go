package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// 重排模型与向量来源通常是同一个服务商，默认沿用对话模型的端点与密钥。
	defaultRerankModel = "BAAI/bge-reranker-v2-m3"
	// rerank 输入按字符截断。注意它与切块尺寸的关系：片段被切得比这里更长时，
	// 尾部内容不会参与打分，答案落在块尾就可能被阈值误杀。
	rerankMaxRunes = 1200
)

// rerankConfig 描述重排连接设置。分数阈值不在这里——它属于检索策略，
// 由 rag.Tuning.MinScore 统一管理，避免同一个阈值存在两个来源。
type rerankConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// rerankConfigFor 从当前生效的模型配置派生重排配置：
// 来源 / 手工配置 / RERANK_* 环境变量的优先级与对话、向量一致。
func (s *Server) rerankConfigFor(ctx context.Context) rerankConfig {
	if strings.EqualFold(strings.TrimSpace(env("RERANK_ENABLED", "true")), "false") {
		return rerankConfig{}
	}
	mc := s.modelConfigFor(ctx)
	cfg := rerankConfig{
		BaseURL: strings.TrimRight(mc.RerankBaseURL, "/"),
		APIKey:  mc.RerankAPIKey,
		Model:   mc.RerankModel,
	}
	if cfg.BaseURL == "" || missingModelCredential(cfg.BaseURL, cfg.APIKey) {
		return rerankConfig{}
	}
	return cfg
}

// testRerankWith 用给定配置发一次最小重排请求，供设置页「测试连接」使用。
func (s *Server) testRerankWith(ctx context.Context, cfg modelConfig) error {
	base := strings.TrimRight(cfg.RerankBaseURL, "/")
	if base == "" {
		return errors.New("重排服务未配置：请选择来源或填写 Base URL")
	}
	if missingModelCredential(base, cfg.RerankAPIKey) {
		return errors.New("重排服务缺少 API Key")
	}
	scores, err := s.rerankRequest(ctx, base, cfg.RerankAPIKey, cfg.RerankModel, "连接测试",
		[]string{"这是用于验证重排服务连通性的示例文本。"})
	if err != nil {
		return err
	}
	if len(scores) == 0 {
		return errors.New("重排接口没有返回结果")
	}
	return nil
}

type rerankScore struct {
	Index int
	Score float64
}

// rerankRequest 是统一的重排调用：/rerank 端点，输入 query + 文档数组。
func (s *Server) rerankRequest(ctx context.Context, base, key, model, query string, docs []string) ([]rerankScore, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":     model,
		"query":     query,
		"documents": docs,
		"top_n":     len(docs),
	})
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/rerank", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	res, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("rerank API %d: %s", res.StatusCode, truncateRunes(string(raw), 200))
	}
	var parsed struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([]rerankScore, 0, len(parsed.Results))
	for _, item := range parsed.Results {
		out = append(out, rerankScore{Index: item.Index, Score: item.RelevanceScore})
	}
	return out, nil
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(env(key, "")), 64); err == nil && v > 0 {
		return v
	}
	return fallback
}
