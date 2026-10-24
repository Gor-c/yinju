package app

// 会话记忆：三层结构。
//
//	L1 工作记忆 —— 最近 N 条原文（retrieval_settings.memory_messages 控制）
//	L2 滚动摘要 —— 把滑出窗口的部分压成一段文字，存在 agent_conversations.summary
//	L3 原子事实 —— 用户明确说过的约束/偏好/结论，存在 agent_memory_facts
//
// 为什么必须分层：只回放最近 N 条，二十轮之前说过的「只看 2 号线」会静默消失，
// 而用户完全看不出模型已经忘了。摘要把这件事变得可见（记忆页能看到），
// 事实则保证「约束」这种最该长期有效的东西不会随摘要一起被稀释。
//
// 一条硬规则：摘要和事实都只是「理解上下文」的材料，不是证据。
// 它们是有损压缩，拿它们当引用来源就是二手引用，所以注入时明确标注不可引用。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/agent"
	"knowledge-base/internal/logging"
)

const (
	memoryFactLimit      = 20   // 单个会话里「当前有效」的事实上限
	memoryFactHistory    = 10   // 记忆页展示的失效版本数
	memoryFactStoreLimit = 60   // 落库上限，含失效版本，超出才真删
	memorySummaryMax     = 1600 // 摘要落库上限（字符）
	memoryCompactMinMsgs = 4    // 至少攒够 4 条待压缩消息才调模型，避免每轮都花一次调用
	memoryCompactRunes   = 700  // 压缩时单条消息截断，控制输入长度
)

type memoryFact struct {
	ID              int64      `json:"id"`
	Kind            string     `json:"kind"`
	Topic           string     `json:"topic"`
	Content         string     `json:"content"`
	Confidence      float64    `json:"confidence"`
	SourceMessageID int64      `json:"source_message_id"`
	Active          bool       `json:"active"`
	SupersededBy    *int64     `json:"superseded_by,omitempty"`
	SupersededAt    *time.Time `json:"superseded_at,omitempty"`
}

type memorySnapshot struct {
	Summary     string
	SummaryUpto int64
	SummaryAt   *time.Time
	// Facts 只含当前有效的事实；History 是被后来者取代的旧版本，
	// 保留下来是为了回答「为什么模型现在这么答」。
	Facts   []memoryFact
	History []memoryFact
}

func (s *Server) ensureMemorySchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	stmts := []string{
		`ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS summary text NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS summary_upto bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE agent_conversations ADD COLUMN IF NOT EXISTS summary_at timestamptz`,
		`CREATE TABLE IF NOT EXISTS agent_memory_facts (
			id bigserial PRIMARY KEY,
			conversation_id text NOT NULL REFERENCES agent_conversations(conversation_id) ON DELETE CASCADE,
			kind text NOT NULL DEFAULT 'constraint',
			content text NOT NULL,
			source_message_id bigint NOT NULL DEFAULT 0,
			confidence real NOT NULL DEFAULT 0.8,
			created_at timestamptz NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE agent_memory_facts ADD COLUMN IF NOT EXISTS topic text NOT NULL DEFAULT ''`,
		`ALTER TABLE agent_memory_facts ADD COLUMN IF NOT EXISTS active boolean NOT NULL DEFAULT true`,
		`ALTER TABLE agent_memory_facts ADD COLUMN IF NOT EXISTS superseded_by bigint`,
		`ALTER TABLE agent_memory_facts ADD COLUMN IF NOT EXISTS superseded_at timestamptz`,
		`CREATE UNIQUE INDEX IF NOT EXISTS agent_memory_facts_unique ON agent_memory_facts(conversation_id, content)`,
		`CREATE INDEX IF NOT EXISTS agent_memory_facts_conv_idx ON agent_memory_facts(conversation_id, id DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("memory_schema_failed", "error", err)
			return
		}
	}
}

// loadMemory 读取当前会话的记忆快照。读不到时返回空值，不阻断回答。
func (s *Server) loadMemory(ctx context.Context, conversationID string) memorySnapshot {
	if s.db == nil || conversationID == "" {
		return memorySnapshot{}
	}
	var snap memorySnapshot
	err := s.db.QueryRow(ctx, `SELECT summary, summary_upto, summary_at
		FROM agent_conversations WHERE conversation_id = $1`, conversationID).
		Scan(&snap.Summary, &snap.SummaryUpto, &snap.SummaryAt)
	if err != nil {
		return memorySnapshot{}
	}

	rows, err := s.db.Query(ctx, `SELECT id, kind, topic, content, confidence, source_message_id,
			active, superseded_by, superseded_at
		FROM agent_memory_facts WHERE conversation_id = $1 ORDER BY id DESC LIMIT 60`, conversationID)
	if err != nil {
		return snap
	}
	defer rows.Close()

	active := []memoryFact{}
	history := []memoryFact{}
	for rows.Next() {
		var f memoryFact
		if err := rows.Scan(&f.ID, &f.Kind, &f.Topic, &f.Content, &f.Confidence,
			&f.SourceMessageID, &f.Active, &f.SupersededBy, &f.SupersededAt); err != nil {
			continue
		}
		if f.Active {
			active = append(active, f)
		} else if len(history) < memoryFactHistory {
			history = append(history, f)
		}
	}
	// 展示时按时间正序更符合阅读习惯；历史版本保持倒序（最近的失效在前）。
	for i, j := 0, len(active)-1; i < j; i, j = i+1, j-1 {
		active[i], active[j] = active[j], active[i]
	}
	snap.Facts = active
	snap.History = history
	return snap
}

// renderMemoryBlock 把记忆渲染成注入 system prompt 的文本。
// 空记忆返回空串，调用方据此跳过拼接。
func renderMemoryBlock(snap memorySnapshot) string {
	summary := strings.TrimSpace(snap.Summary)
	if summary == "" && len(snap.Facts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n---\n【会话记忆｜自行参考，不得作为答案依据，也不得出现在引用编号里】\n")
	b.WriteString("以下内容来自本次对话更早的部分，可能有遗漏或失真。它只能帮你理解用户此前的说法，")
	b.WriteString("不能替代本轮检索到的片段；两者冲突时以检索结果为准。\n")

	if len(snap.Facts) > 0 {
		b.WriteString("\n用户当前有效的约束与偏好（优先遵守；与更早的说法冲突时以这里为准）：\n")
		for _, f := range snap.Facts {
			b.WriteString("- ")
			b.WriteString(f.Content)
			b.WriteString("\n")
		}
	}
	if summary != "" {
		b.WriteString("\n更早对话的摘要：\n")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	return b.String()
}

// scheduleMemoryCompaction 在回答完成后异步压缩，不阻塞也不影响本次回答。
// 用独立 context：请求上下文此时已被取消，但摘要仍需要写成功。
func (s *Server) scheduleMemoryCompaction(conversationID string) {
	if s.db == nil || conversationID == "" || !s.memorySummaryEnabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := s.compactMemory(ctx, conversationID); err != nil {
			logging.App().Warn("memory_compact_failed", "conversation_id", conversationID, "error", err)
		}
	}()
}

// pendingMsg 是待压缩的一条消息，也被来源匹配复用。
type pendingMsg struct {
	ID      int64
	Role    string
	Content string
}

type compactedMemory struct {
	Summary string `json:"summary"`
	Facts   []struct {
		Kind       string   `json:"kind"`
		Topic      string   `json:"topic"`
		Content    string   `json:"content"`
		Confidence float64  `json:"confidence"`
		Supersedes []string `json:"supersedes"`
	} `json:"facts"`
}

// compactMemory 把「已滑出窗口且尚未被摘要覆盖」的消息压进摘要。
// 返回本次覆盖的消息条数，0 表示没有需要压缩的内容。
func (s *Server) compactMemory(ctx context.Context, conversationID string) (int, error) {
	window := s.memoryMessages()
	if window <= 0 {
		return 0, nil
	}

	var summary string
	var summaryUpto int64
	if err := s.db.QueryRow(ctx, `SELECT summary, summary_upto FROM agent_conversations
		WHERE conversation_id = $1`, conversationID).Scan(&summary, &summaryUpto); err != nil {
		return 0, err
	}

	// 窗口起点：最近 window 条消息里最小的一条 id。只有比它更早的才需要压缩。
	var windowStart int64
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(MIN(id), 0) FROM (
			SELECT id FROM agent_messages
			WHERE conversation_id = $1 AND role IN ('user','assistant') AND content <> ''
			ORDER BY id DESC LIMIT $2
		) w`, conversationID, window).Scan(&windowStart); err != nil {
		return 0, err
	}
	if windowStart <= 0 {
		return 0, nil
	}

	rows, err := s.db.Query(ctx, `SELECT id, role, content FROM agent_messages
		WHERE conversation_id = $1 AND role IN ('user','assistant') AND content <> ''
			AND id > $2 AND id < $3
		ORDER BY id`, conversationID, summaryUpto, windowStart)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	pending := []pendingMsg{}
	for rows.Next() {
		var m pendingMsg
		if err := rows.Scan(&m.ID, &m.Role, &m.Content); err == nil {
			pending = append(pending, m)
		}
	}
	if len(pending) < memoryCompactMinMsgs {
		return 0, nil
	}

	client, err := s.newAgentModel()
	if err != nil {
		return 0, err
	}

	var transcript strings.Builder
	if strings.TrimSpace(summary) != "" {
		transcript.WriteString("已有摘要（在此基础上前向合并，不要重复旧内容）：\n")
		transcript.WriteString(summary)
		transcript.WriteString("\n\n新增对话：\n")
	}
	for _, m := range pending {
		role := "用户"
		if m.Role == "assistant" {
			role = "助手"
		}
		transcript.WriteString(role + "：" + truncateRunes(m.Content, memoryCompactRunes) + "\n")
	}

	prompt := []agent.Message{
		{Role: "system", Content: `你在维护一段产品问答会话的长期记忆。请输出 JSON，不要输出任何其他文字：
{"summary": "压缩后的摘要（不超过 400 字）", "facts": [{"kind": "constraint|preference|decision", "topic": "scope|format|identity|language|focus", "content": "一句话", "confidence": 0.9, "supersedes": ["被这条取代的旧说法原文"]}]}

规则：
1. summary 要保留：用户想解决什么问题、已经确认过的结论、尚未解决的分歧。不要复述具体数字条款，那些在原文里已有。
2. facts 只记录用户明确说过的约束、偏好、结论，例如「只看 2 号线」「回答用表格」「我是项目经理」。不要记录助手的推测。
3. topic 必须从这五个里选一个，它决定「哪些说法是同一件事」：
   - scope：看哪些资料/范围（如「只看消防部分」）
   - format：回答格式与长度（如「不超过三条」「用表格」）
   - identity：用户身份或角色
   - language：语言与术语偏好
   - focus：持续关注的主题
4. 如果这次对话里用户改变了之前的要求（例如先说「只看消防部分」，后说「现在也可以看质量部分了」），
   新 facts 要写的是**改变后**的说法，并把旧说法的原文放进 supersedes 数组。没有冲突就留空数组。
5. 没有新的 facts 就返回空数组，不要为了凑数编造；也不要把同一条重复输出。
6. summary 要用陈述句，不要出现「用户问」「助手答」这类对话体。`},
		{Role: "user", Content: transcript.String()},
	}

	response, _, err := client.Complete(ctx, prompt, nil, nil)
	if err != nil {
		return 0, err
	}
	parsed, err := parseCompactedMemory(response.Content)
	if err != nil {
		return 0, err
	}

	newSummary := truncateRunes(strings.TrimSpace(parsed.Summary), memorySummaryMax)
	lastID := pending[len(pending)-1].ID
	if newSummary != "" {
		if _, err := s.db.Exec(ctx, `UPDATE agent_conversations
			SET summary = $2, summary_upto = $3, summary_at = now() WHERE conversation_id = $1`,
			conversationID, newSummary, lastID); err != nil {
			return 0, err
		}
	}

	added, superseded := 0, 0
	for _, f := range parsed.Facts {
		content := strings.TrimSpace(f.Content)
		if len([]rune(content)) < 4 {
			continue
		}
		kind := strings.TrimSpace(f.Kind)
		if kind == "" {
			kind = "constraint"
		}
		confidence := f.Confidence
		if confidence <= 0 || confidence > 1 {
			confidence = 0.8
		}
		// 来源指向「真正说出这句话的那条消息」，而不是这批的最后一条——
		// 页面上的「第 N 条」要能跳回原话，指错了就等于没有留痕。
		sourceID := bestSourceMessage(content, pending)
		_, dropped, err := s.upsertMemoryFact(ctx, conversationID, kind,
			normalizeFactTopic(f.Topic), truncateRunes(content, 200), sourceID, confidence, f.Supersedes)
		if err != nil {
			logging.App().Warn("memory_fact_write_failed", "error", err)
			continue
		}
		added++
		superseded += dropped
	}
	if added > 0 {
		// 有效事实限量，避免越滚越多把 prompt 撑满；失效版本留作追溯，
		// 只在整个会话的事实存量过大时清理最旧的。
		_, _ = s.db.Exec(ctx, `UPDATE agent_memory_facts
			SET active = false, superseded_at = now()
			WHERE conversation_id = $1 AND active AND id NOT IN (
				SELECT id FROM agent_memory_facts WHERE conversation_id = $1 AND active ORDER BY id DESC LIMIT $2
			)`, conversationID, memoryFactLimit)
		_, _ = s.db.Exec(ctx, `DELETE FROM agent_memory_facts WHERE conversation_id = $1 AND id NOT IN (
			SELECT id FROM agent_memory_facts WHERE conversation_id = $1 ORDER BY id DESC LIMIT $2
		)`, conversationID, memoryFactStoreLimit)
	}

	logging.App().Info("memory_compacted", "conversation_id", conversationID,
		"messages", len(pending), "summary_runes", len([]rune(newSummary)),
		"facts", added, "superseded", superseded)
	return len(pending), nil
}

// bestSourceMessage 用二元组重合度找出事实对应的原话。
// 不追求精确语义匹配，只要有明显更高的重合就够把「第 N 条」指对；
// 完全匹配不上时退回本批最后一条，至少落在压缩区间内。
func bestSourceMessage(fact string, pending []pendingMsg) int64 {
	if len(pending) == 0 {
		return 0
	}
	want := bigramSet(fact)
	best := pending[len(pending)-1].ID
	bestScore := -1
	for _, m := range pending {
		if m.Role != "user" {
			continue
		}
		score := 0
		for gram := range bigramSet(m.Content) {
			if _, ok := want[gram]; ok {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			best = m.ID
		}
	}
	return best
}

func bigramSet(text string) map[string]struct{} {
	runes := []rune(strings.ToLower(text))
	out := make(map[string]struct{}, len(runes))
	for i := 0; i+1 < len(runes); i++ {
		a, b := runes[i], runes[i+1]
		if isSpaceRune(a) || isSpaceRune(b) {
			continue
		}
		out[string([]rune{a, b})] = struct{}{}
	}
	return out
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '　'
}

// parseCompactedMemory 容错解析：模型偶尔会在 JSON 前后加解释或代码围栏。
func parseCompactedMemory(raw string) (compactedMemory, error) {
	text := strings.TrimSpace(raw)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			text = text[start : end+1]
		}
	}
	var out compactedMemory
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return out, fmt.Errorf("parse memory json: %w", err)
	}
	return out, nil
}

// memorySummaryEnabled 默认开启；关闭后只剩「最近 N 条回放」。
func (s *Server) memorySummaryEnabled() bool {
	cfg := s.runtimeConfig()
	return cfg.MemorySummary
}

// rebuildMemory 手动重建：把当前窗口之前的全部消息一次性压缩。
// 页面上点「重建摘要」时调用，属于运维动作，允许同步等几秒。
func (s *Server) rebuildMemory(ctx context.Context, conversationID string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("database is not configured")
	}
	// 清空覆盖标记，让 compactMemory 重新处理窗口外的全部消息。
	if _, err := s.db.Exec(ctx, `UPDATE agent_conversations
		SET summary = '', summary_upto = 0, summary_at = NULL WHERE conversation_id = $1`, conversationID); err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM agent_memory_facts WHERE conversation_id = $1`, conversationID); err != nil {
		return 0, err
	}
	return s.compactMemory(ctx, conversationID)
}

// normalizeFactTopic 把模型给的主题收敛到固定几个值。
// 收敛不了就返回空串：空主题不参与自动取代——错杀一条有效约束，
// 比两条并存更难被发现。
func normalizeFactTopic(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "scope", "范围", "知识范围":
		return "scope"
	case "format", "格式", "输出格式":
		return "format"
	case "identity", "身份", "角色":
		return "identity"
	case "language", "语言", "术语":
		return "language"
	case "focus", "关注", "关注点":
		return "focus"
	}
	return ""
}

// upsertMemoryFact 写入一条事实，并让它在同主题上取代旧版本。
//
// 取代不是删除：旧记录保留 active=false 与 superseded_by，记忆页能看到
// 「这条为什么失效、被谁取代」。用户改主意是常态，抹掉痕迹就无法解释
// 模型为什么突然换了答法。
func (s *Server) upsertMemoryFact(ctx context.Context, conversationID, kind, topic, content string,
	sourceID int64, confidence float64, supersedes []string) (int64, int, error) {

	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO agent_memory_facts(
			conversation_id, kind, topic, content, source_message_id, confidence, active)
		VALUES($1,$2,$3,$4,$5,$6,true)
		ON CONFLICT (conversation_id, content)
		DO UPDATE SET active = true, superseded_by = NULL, superseded_at = NULL,
			confidence = GREATEST(agent_memory_facts.confidence, EXCLUDED.confidence),
			topic = CASE WHEN EXCLUDED.topic <> '' THEN EXCLUDED.topic ELSE agent_memory_facts.topic END,
			created_at = now()
		RETURNING id`,
		conversationID, kind, topic, content, sourceID, confidence).Scan(&id)
	if err != nil {
		return 0, 0, err
	}

	dropped := 0
	// 同主题视为同一件事：新说法生效，旧说法失效。
	if topic != "" {
		tag, err := s.db.Exec(ctx, `UPDATE agent_memory_facts
			SET active = false, superseded_by = $2, superseded_at = now()
			WHERE conversation_id = $1 AND active AND topic = $3 AND id <> $2`,
			conversationID, id, topic)
		if err == nil {
			dropped += int(tag.RowsAffected())
		}
	}
	// 模型显式指名的旧说法：跨主题也照办，例如「以后别用表格了」。
	for _, old := range supersedes {
		old = truncateRunes(strings.TrimSpace(old), 200)
		if len([]rune(old)) < 4 {
			continue
		}
		tag, err := s.db.Exec(ctx, `UPDATE agent_memory_facts
			SET active = false, superseded_by = $2, superseded_at = now()
			WHERE conversation_id = $1 AND active AND id <> $2 AND content = $3`,
			conversationID, id, old)
		if err == nil {
			dropped += int(tag.RowsAffected())
		}
	}
	if dropped > 0 {
		logging.App().Info("memory_fact_superseded", "conversation_id", conversationID,
			"new_fact_id", id, "topic", topic, "superseded", dropped)
	}
	return id, dropped, nil
}

// conversationMemory 供记忆页读取：摘要、事实、当前窗口参数。
func (s *Server) conversationMemory(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	conversationID := c.Param("id")
	snap := s.loadMemory(c, conversationID)
	c.JSON(http.StatusOK, gin.H{
		"conversation_id": conversationID,
		"summary":         snap.Summary,
		"summary_upto":    snap.SummaryUpto,
		"summary_at":      snap.SummaryAt,
		"facts":           snap.Facts,
		"history":         snap.History,
		"enabled":         s.memorySummaryEnabled(),
		"window": gin.H{
			"messages": s.memoryMessages(),
			"runes":    s.memoryRunes(),
		},
	})
}

// rebuildConversationMemory 清空并重建摘要与事实。
// 属于运维动作：会调一次模型，允许同步等待。
func (s *Server) rebuildConversationMemory(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	conversationID := c.Param("id")
	compacted, err := s.rebuildMemory(c, conversationID)
	if err != nil {
		fail(c, err)
		return
	}
	snap := s.loadMemory(c, conversationID)
	c.JSON(http.StatusOK, gin.H{
		"compacted": compacted,
		"summary":   snap.Summary,
		"facts":     snap.Facts,
	})
}
