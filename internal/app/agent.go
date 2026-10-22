package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

// Configuration errors are safe to return to the client so local setup
// problems are actionable instead of a generic 500.
var (
	errEmbeddingConfig = errors.New("embedding 服务未配置：请在「模型设置」页填写 base_url 与 api_key，或设置 EMBED_BASE_URL/EMBED_API_KEY")
	errChatConfig      = errors.New("chat 模型未配置：请在「模型设置」页填写 base_url 与 api_key，或设置 LLM_BASE_URL/LLM_API_KEY")
)

type askRequest struct {
	UnitID   string `json:"unit_id"`
	Question string `json:"question"`
	TopK     int    `json:"top_k"`
}

type connector struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}
type expert struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	SystemPrompt string   `json:"system_prompt"`
	Tools        []string `json:"tools,omitempty"`
	Source       string   `json:"source,omitempty"`
}
type skill struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Instructions string   `json:"instructions"`
	Tools        []string `json:"tools,omitempty"`
	Source       string   `json:"source,omitempty"`
}

type citation struct {
	// SourceType distinguishes knowledge-base documents from external web pages
	// so the UI and the model never mix them up.
	SourceType string  `json:"source_type,omitempty"`
	UnitID     string  `json:"unit_id,omitempty"`
	UnitName   string  `json:"unit_name,omitempty"`
	Filename   string  `json:"filename,omitempty"`
	DocType    string  `json:"doc_type,omitempty"`
	Title      string  `json:"title,omitempty"`
	URL        string  `json:"url,omitempty"`
	Site       string  `json:"site,omitempty"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance,omitempty"`
}

const (
	maxQuestionRunes   = 8000
	maxHistoryMessages = 20
	maxHistoryRunes    = 4000
)

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func (s *Server) ask(c *gin.Context) {
	started := time.Now()
	var in askRequest
	if e := c.ShouldBindJSON(&in); e != nil || in.UnitID == "" || in.Question == "" {
		c.JSON(400, gin.H{"error": "unit_id and question are required"})
		return
	}
	if len([]rune(in.Question)) > maxQuestionRunes {
		c.JSON(400, gin.H{"error": fmt.Sprintf("question exceeds %d characters", maxQuestionRunes)})
		return
	}
	if !s.ready(c) {
		return
	}
	if s.vectors == nil {
		c.JSON(503, gin.H{"error": "vector store is not configured"})
		return
	}
	exists, e := s.unitExists(c, in.UnitID)
	if e != nil {
		fail(c, e)
		return
	}
	if !exists {
		c.JSON(404, gin.H{"error": "unit not found: " + in.UnitID})
		return
	}
	qv, e := s.embedContext(c, []string{in.Question})
	if e != nil {
		fail(c, e)
		return
	}
	if len(qv) == 0 {
		c.JSON(502, gin.H{"error": "embedding API returned no vectors"})
		return
	}
	k := in.TopK
	if k < 1 || k > 20 {
		k = 5
	}
	docIDs, e := s.docIDsForUnits(c, []string{in.UnitID})
	if e != nil {
		fail(c, e)
		return
	}
	matches, e := s.vectors.Search(c, docIDs, qv[0], k)
	if e != nil {
		fail(c, e)
		return
	}
	citations := []citation{}
	var contextParts []string
	for _, match := range matches {
		citations = append(citations, citation{Filename: match.Filename, DocType: match.DocType, Content: match.Content, Distance: match.Distance})
		contextParts = append(contextParts, "["+match.Filename+"] "+match.Content)
	}
	answer, e := s.complete(c, "你是知识库分析专家。请仅根据给定资料回答；资料不足时明确说明。", "问题："+in.Question+"\n\n检索资料：\n"+strings.Join(contextParts, "\n\n"))
	if e != nil {
		fail(c, e)
		return
	}
	logging.App().Info("rag_ask_completed",
		"request_id", c.GetString(requestIDKey),
		"unit_id", in.UnitID,
		"top_k", k,
		"citations", len(citations),
		"duration_ms", time.Since(started).Milliseconds(),
	)
	c.JSON(200, gin.H{"answer": answer, "citations": citations})
}

func (s *Server) agentConfig(c *gin.Context) {
	c.JSON(200, gin.H{"experts": s.expertList(), "skills": s.skillList(), "connectors": s.connectorList()})
}
func (s *Server) connectors(c *gin.Context) { c.JSON(200, s.connectorList()) }

// expertList merges built-in/environment experts with definitions uploaded
// through the management page. Uploaded rows win on the same id so a project
// can be tuned without editing environment variables.
func (s *Server) expertList() []expert {
	var base []expert
	if json.Unmarshal([]byte(os.Getenv("AGENT_EXPERTS_JSON")), &base) != nil || len(base) == 0 {
		base = []expert{{ID: "kb-expert", Name: "知识库专家", Description: "基于已选知识库检索并回答", SystemPrompt: "你是知识库助手。基于检索到的资料回答，引用文件名作为出处；资料不足时明确说明，不要编造。", Tools: allAgentToolNames()}, {ID: "general", Name: "通用助手", Description: "通用对话", SystemPrompt: "你是一个有帮助的通用助手。"}}
	}
	uploaded, err := s.loadExpertDefs()
	if err != nil {
		logging.App().Warn("agent_experts_load_failed", "error", err)
		return base
	}
	return mergeExperts(base, uploaded)
}

func (s *Server) skillList() []skill {
	var base []skill
	if json.Unmarshal([]byte(os.Getenv("AGENT_SKILLS_JSON")), &base) != nil || len(base) == 0 {
		base = []skill{
			{ID: "kb-search", Name: "知识库检索", Description: "检索已索引文档并标注出处", Instructions: "优先依据检索到的片段回答，并附上文件名作为出处；资料不足时明确说明。", Tools: []string{"vector_search"}},
			{ID: "kb-review", Name: "记录核对", Description: "核对结构化记录与文档结论", Instructions: "核对时列出依据、差异和建议确认的事项，不要编造文档中不存在的信息。", Tools: []string{"query_hazards", "audit_history"}},
		}
	}
	uploaded, err := s.loadSkillDefs()
	if err != nil {
		logging.App().Warn("agent_skills_load_failed", "error", err)
		return base
	}
	return mergeSkills(base, uploaded)
}

func (s *Server) connectorList() []connector {
	var out []connector
	_ = json.Unmarshal([]byte(env("MCP_CONNECTORS_JSON", "[]")), &out)
	return out
}

func (s *Server) chat(c *gin.Context) {
	started := time.Now()
	in, ok := bindAgentChat(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), agentRunTimeout)
	defer cancel()

	runID := newRunID()
	answer, e := s.runAgent(ctx, in, runID, nil)
	if e != nil {
		s.respondAgentError(c, e)
		return
	}
	logging.App().Info("agent_chat_completed",
		"request_id", c.GetString(requestIDKey),
		"run_id", runID,
		"unit_id", in.UnitID,
		"expert", in.Expert,
		"skills", len(answer.Skills),
		"citations", len(answer.Citations),
		"steps", answer.Steps,
		"duration_ms", time.Since(started).Milliseconds(),
	)
	c.JSON(http.StatusOK, gin.H{
		"answer":          answer.Answer,
		"expert":          in.Expert,
		"skills":          answer.Skills,
		"citations":       answer.Citations,
		"run_id":          runID,
		"steps":           answer.Steps,
		"conversation_id": answer.ConversationID,
	})
}

func (s *Server) embed(texts []string) ([][]float64, error) {
	return s.embedContext(context.Background(), texts)
}

func (s *Server) embedContext(ctx context.Context, texts []string) ([][]float64, error) {
	cfg := s.modelConfigFor(ctx)
	return s.embeddingRequest(ctx, "/embeddings", gin.H{"model": cfg.EmbedModel, "input": texts})
}

func (s *Server) embeddingRequest(ctx context.Context, endpoint string, body any) ([][]float64, error) {
	return s.embeddingRequestWith(ctx, s.modelConfigFor(ctx), endpoint, body)
}

// embeddingRequestWith 用显式配置发请求，供「来源测试」在不切换生效配置时验证连接。
// 429/5xx/网络错误会自动重试，避免一条限流响应就让整份文档索引失败。
func (s *Server) embeddingRequestWith(ctx context.Context, cfg modelConfig, endpoint string, body any) ([][]float64, error) {
	base := strings.TrimRight(cfg.EmbedBaseURL, "/")
	key := cfg.EmbedAPIKey
	if base == "" || missingModelCredential(base, key) {
		return nil, errEmbeddingConfig
	}
	payload, _ := json.Marshal(body)

	var lastErr error
	for attempt := 1; attempt <= maxModelAttempts; attempt++ {
		out, err := s.embeddingOnce(ctx, base, key, endpoint, payload)
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == maxModelAttempts || !retryableUpstream(err) {
			break
		}
		if !waitModelRetry(ctx, attempt) {
			break
		}
	}
	return nil, lastErr
}

func (s *Server) embeddingOnce(ctx context.Context, base, key, endpoint string, payload []byte) ([][]float64, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", base+endpoint, bytes.NewReader(payload))
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
		return nil, &upstreamError{Kind: "embedding", Status: res.StatusCode, Body: truncateRunes(string(raw), 300)}
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	out := make([][]float64, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Index >= 0 && d.Index < len(out) {
			out[d.Index] = d.Embedding
		}
	}
	return out, nil
}

func (s *Server) complete(ctx context.Context, sys, user string) (string, error) {
	return s.completeMessages(ctx, []gin.H{{"role": "system", "content": sys}, {"role": "user", "content": user}})
}

func (s *Server) completeMessages(ctx context.Context, messages []gin.H) (string, error) {
	return s.completeMessagesWith(ctx, s.modelConfigFor(ctx), messages)
}

// completeMessagesWith 用显式配置发请求，供「来源测试」使用。
func (s *Server) completeMessagesWith(ctx context.Context, cfg modelConfig, messages []gin.H) (string, error) {
	base := strings.TrimRight(cfg.LLMBaseURL, "/")
	key := cfg.LLMAPIKey
	if base == "" || missingModelCredential(base, key) {
		return "", errChatConfig
	}
	b, _ := json.Marshal(gin.H{"model": cfg.LLMModel, "messages": messages, "temperature": 0.2})
	req, e := http.NewRequestWithContext(ctx, "POST", base+"/chat/completions", bytes.NewReader(b))
	if e != nil {
		return "", e
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, e := s.http.Do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("chat API %d: %s", res.StatusCode, string(raw))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if e = json.Unmarshal(raw, &parsed); e != nil {
		return "", e
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("chat API returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}
