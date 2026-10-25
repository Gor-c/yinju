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
	UnitID   string   `json:"unit_id"`
	UnitIDs  []string `json:"unit_ids"`
	Question string   `json:"question"`
	TopK     int      `json:"top_k"`
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
	SourceType string `json:"source_type,omitempty"`
	UnitID     string `json:"unit_id,omitempty"`
	UnitName   string `json:"unit_name,omitempty"`
	Filename   string `json:"filename,omitempty"`
	DocType    string `json:"doc_type,omitempty"`
	// DocID / ChunkIndex 用于前端把引用定位到原文的具体片段。
	DocID      string  `json:"doc_id,omitempty"`
	ChunkIndex int     `json:"chunk_index"`
	Title      string  `json:"title,omitempty"`
	URL        string  `json:"url,omitempty"`
	Site       string  `json:"site,omitempty"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance,omitempty"`
	// Score 是重排分（rerank）或融合分，前端只在 hover 时展示。
	Score float64 `json:"score,omitempty"`
	// Index 是服务端分配的引用编号（1 起）。/ask 按返回顺序写入，
	// 与 /agent 的工具结果 index 语义一致；其他路径不填则不输出。
	Index int `json:"index,omitempty"`
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
	// 参数与 /retrieve 对齐：unit_ids 为数组，unit_id 单值兼容保留。
	if e := c.ShouldBindJSON(&in); e != nil || strings.TrimSpace(in.Question) == "" {
		c.JSON(400, gin.H{"error": "question is required"})
		return
	}
	units := cleanSkillIDs(in.UnitIDs)
	if len(units) == 0 && strings.TrimSpace(in.UnitID) != "" {
		units = []string{strings.TrimSpace(in.UnitID)}
	}
	if len(units) == 0 {
		c.JSON(400, gin.H{"error": "unit_id or unit_ids is required"})
		return
	}
	in.Question = strings.TrimSpace(in.Question)
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
	for _, id := range units {
		exists, e := s.unitExists(c, id)
		if e != nil {
			fail(c, e)
			return
		}
		if !exists {
			c.JSON(404, gin.H{"error": "unit not found: " + id})
			return
		}
	}
	k := in.TopK
	if k < 1 || k > 20 {
		k = 5
	}
	docIDs, e := s.docIDsForUnits(c, units)
	if e != nil {
		fail(c, e)
		return
	}
	matches, e := s.hybridSearch(c, docIDs, in.Question, k)
	if e != nil {
		fail(c, e)
		return
	}
	citations := []citation{}
	var contextParts []string
	// 文档是全局实体、可被多个库引用，所以只有单库提问时才标注来源库。
	singleUnit := ""
	if len(units) == 1 {
		singleUnit = units[0]
	}
	for i, match := range matches {
		citations = append(citations, citation{
			SourceType: "document", UnitID: singleUnit, Filename: match.Filename, DocType: match.DocType,
			DocID: match.DocID, ChunkIndex: match.ChunkIndex, Content: match.Content,
			Distance: match.Distance, Score: match.Score, Index: i + 1,
		})
		// 编号由服务端按顺序分配，模型只能引用这些编号，不能自己数。
		contextParts = append(contextParts, fmt.Sprintf("[%d] 文件名：%s\n%s", i+1, match.Filename, match.Content))
	}
	// 严格模式：没有达到阈值的依据时直接说明未找到，不让模型自由发挥。
	if len(matches) == 0 {
		logging.App().Info("rag_ask_no_evidence",
			"request_id", c.GetString(requestIDKey), "unit_ids", units,
			"duration_ms", time.Since(started).Milliseconds())
		c.JSON(200, gin.H{"answer": noEvidenceAnswer, "citations": []citation{}, "no_evidence": true, "unit_ids": units})
		return
	}
	answer, e := s.complete(c,
		`你是知识库分析专家。严格按以下规则回答：
1. 只依据给定资料；资料不足时明确说明，不得用常识、经验或猜测补全。
2. 每处结论后用方括号标注来源编号，形如「牵引力不得超过 80%[2]」，编号只能取自资料的 [n] 标记；同一段话引用多处就并排标注 [2][5]。
3. 不要编造资料中未出现的数字、时间与责任主体。
4. 正文里不要罗列来源清单，界面已按编号展示引用。`,
		"问题："+in.Question+"\n\n检索资料：\n"+strings.Join(contextParts, "\n\n"))
	if e != nil {
		fail(c, e)
		return
	}
	logging.App().Info("rag_ask_completed",
		"request_id", c.GetString(requestIDKey),
		"unit_ids", units,
		"top_k", k,
		"citations", len(citations),
		"duration_ms", time.Since(started).Milliseconds(),
	)
	c.JSON(200, gin.H{"answer": answer, "citations": citations, "unit_ids": units})
}

// retrieve 只做检索、不调用大模型，用于评测、调试与「检索预览」。
// 与 Agent 工具走同一条混合检索链路，因此评测结果能代表真实问答的召回。
func (s *Server) retrieve(c *gin.Context) {
	var in struct {
		UnitID  string   `json:"unit_id"`
		UnitIDs []string `json:"unit_ids"`
		Query   string   `json:"query"`
		TopK    int      `json:"top_k"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || strings.TrimSpace(in.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}
	units := in.UnitIDs
	if len(units) == 0 && in.UnitID != "" {
		units = []string{in.UnitID}
	}
	if len(units) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unit_id or unit_ids is required"})
		return
	}
	if !s.ready(c) {
		return
	}
	k := in.TopK
	if k < 1 || k > 20 {
		k = 5
	}
	docIDs, err := s.docIDsForUnits(c, units)
	if err != nil {
		fail(c, err)
		return
	}
	started := time.Now()
	matches, stats, err := s.hybridSearchWithStats(c, docIDs, in.Query, k)
	if err != nil {
		fail(c, err)
		return
	}
	items := make([]gin.H, 0, len(matches))
	for _, m := range matches {
		items = append(items, gin.H{
			"doc_id": m.DocID, "filename": m.Filename, "doc_type": m.DocType,
			"chunk_index": m.ChunkIndex, "content": m.Content,
			"distance": m.Distance, "score": m.Score,
		})
	}
	// 分层计数随结果一起返回：改阈值之后，光看「命中几条」不足以判断
	// 改对了没有，得知道它卡在哪一层。
	c.JSON(http.StatusOK, gin.H{
		"query": in.Query, "unit_ids": units, "top_k": k,
		"count": len(items), "matches": items, "no_evidence": len(items) == 0,
		"stats": gin.H{
			"vector_hits": stats.VectorHits, "keyword_hits": stats.KeywordHits,
			"fused": stats.Fused, "reranked": stats.Reranked, "kept": len(items),
			"degraded": stats.Degraded, "doc_count": len(docIDs),
		},
		"duration_ms": time.Since(started).Milliseconds(),
	})
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
		"expert":          answer.Expert,
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
