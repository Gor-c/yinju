package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

// 知识沉淀：把「这次问答解决了什么」留下来，而不是只把它拼进下一次的 prompt。
//
// 关键边界：模型的回答**不是知识**。所以这里有一条状态机，
// pending →（人工采纳）→ 进入索引队列；pending →（忽略）→ dismissed；
// 多条 pending →（整理）→ 一条 note，原文标记 merged。
// 只有被采纳的内容才会进入向量库，未采纳的永远只是草稿、不参与检索。
// 这样「沉淀」和「知识」之间始终有一道闸门，模型输出不会自我强化。

const (
	kbDraftKindQA   = "qa"
	kbDraftKindNote = "note"
	// 沉淀入库文档的 doc_type。它同时是一个「不再作为沉淀依据」的标记：
	// 否则问答 → 沉淀 → 再被检索 → 再沉淀，会形成沉淀的沉淀，误差逐层放大。
	kbDraftDocType = "问答沉淀"
	// 自动整理的触发门槛：低于这个数量不值得花一次模型调用。
	kbAutoDistillMin = 8
	kbDistillBatch   = 12
)

type kbDraftSource struct {
	Filename   string `json:"filename"`
	DocID      string `json:"doc_id,omitempty"`
	ChunkIndex int    `json:"chunk_index"`
}

type kbDraft struct {
	ID             int64           `json:"id"`
	Kind           string          `json:"kind"`
	Status         string          `json:"status"`
	ConversationID string          `json:"conversation_id"`
	RunID          string          `json:"run_id"`
	UnitIDs        []string        `json:"unit_ids"`
	Question       string          `json:"question"`
	Answer         string          `json:"answer"`
	Sources        []kbDraftSource `json:"sources"`
	DocID          string          `json:"doc_id"`
	JobID          string          `json:"job_id"`
	CreatedAt      time.Time       `json:"created_at"`
}

func (s *Server) ensureKnowledgeDraftSchema(ctx context.Context) {
	_, _ = s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS kb_drafts (
		id bigserial PRIMARY KEY,
		kind text NOT NULL DEFAULT 'qa',
		status text NOT NULL DEFAULT 'pending',
		conversation_id text NOT NULL DEFAULT '',
		run_id text NOT NULL DEFAULT '',
		unit_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
		question text NOT NULL DEFAULT '',
		answer text NOT NULL DEFAULT '',
		sources jsonb NOT NULL DEFAULT '[]'::jsonb,
		content_hash text NOT NULL DEFAULT '',
		merged_into bigint NOT NULL DEFAULT 0,
		doc_id text NOT NULL DEFAULT '',
		job_id text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL DEFAULT now(),
		decided_at timestamptz
	)`)
	_, _ = s.db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS kb_drafts_hash_idx ON kb_drafts(content_hash)`)
	_, _ = s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS kb_drafts_status_idx ON kb_drafts(status, kind, created_at DESC)`)
}

// scheduleKnowledgeDraft 在一次问答成功后异步沉淀候选。
// 只收「有文档依据」的回答：纯闲聊、拒答、纯网页答案都不入库。
func (s *Server) scheduleKnowledgeDraft(in agentChatRequest, unitIDs []string, answer agentAnswer) {
	if s.db == nil || len(unitIDs) == 0 {
		return
	}
	text := strings.TrimSpace(answer.Answer)
	if text == "" || text == noEvidenceAnswer {
		return
	}
	question := strings.TrimSpace(in.Message)
	if question == "" {
		return
	}
	sources := make([]kbDraftSource, 0, len(answer.Citations))
	for _, c := range answer.Citations {
		// 网页来源不沉淀：外部内容写进内部知识库会混淆证据等级。
		if c.SourceType == "web" {
			continue
		}
		// 沉淀文档不再作为新草稿的依据。证据只能来自原始文档：
		// 允许「沉淀的沉淀」，等于让模型的上一轮输出间接变成下一轮的事实来源。
		if c.DocType == kbDraftDocType {
			continue
		}
		sources = append(sources, kbDraftSource{Filename: c.Filename, DocID: c.DocID, ChunkIndex: c.ChunkIndex})
	}
	if len(sources) == 0 {
		return
	}
	units := cleanSkillIDs(unitIDs)
	draft := kbDraft{
		Kind: kbDraftKindQA, ConversationID: answer.ConversationID,
		UnitIDs: units, Question: question, Answer: text, Sources: sources,
	}
	sum := sha256.Sum256([]byte(strings.Join(units, ",") + "\n" + question + "\n" + text))
	hash := hex.EncodeToString(sum[:])
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := s.db.Exec(ctx, `INSERT INTO kb_drafts(kind,conversation_id,unit_ids,question,answer,sources,content_hash)
			VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (content_hash) DO NOTHING`,
			kbDraftKindQA, draft.ConversationID, encodeSkillIDs(units), question, text,
			mustJSON(sources), hash)
		if err != nil {
			logging.App().Warn("kb_draft_save_failed", "error", err)
		}
	}()
}

// mustJSON 必须返回 json.RawMessage：pgx 会把 []byte 当成 bytea，
// 直接写 jsonb 列会报类型错误（createUnit 的注释里也是同一个坑）。
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("[]")
	}
	return json.RawMessage(raw)
}

func (s *Server) kbDraftList(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	status := strings.TrimSpace(c.Query("status"))
	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(c, `SELECT id,kind,status,conversation_id,unit_ids,question,answer,sources,doc_id,job_id,created_at
		FROM kb_drafts WHERE ($1='' OR status=$1) ORDER BY created_at DESC LIMIT $2`, status, limit)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	items := []kbDraft{}
	for rows.Next() {
		var d kbDraft
		var unitRaw, sourceRaw []byte
		if err := rows.Scan(&d.ID, &d.Kind, &d.Status, &d.ConversationID, &unitRaw,
			&d.Question, &d.Answer, &sourceRaw, &d.DocID, &d.JobID, &d.CreatedAt); err != nil {
			fail(c, err)
			return
		}
		d.UnitIDs = decodeSkillIDs(unitRaw)
		_ = json.Unmarshal(sourceRaw, &d.Sources)
		if d.Sources == nil {
			d.Sources = []kbDraftSource{}
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		fail(c, err)
		return
	}
	pending := 0
	if err := s.db.QueryRow(c, `SELECT count(*) FROM kb_drafts WHERE status='pending'`).Scan(&pending); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "count": len(items), "pending": pending,
		"auto_distill_min": kbAutoDistillMin})
}

func (s *Server) loadDraft(ctx context.Context, id int64) (kbDraft, error) {
	var d kbDraft
	var unitRaw, sourceRaw []byte
	err := s.db.QueryRow(ctx, `SELECT id,kind,status,conversation_id,unit_ids,question,answer,sources,doc_id,job_id,created_at
		FROM kb_drafts WHERE id=$1`, id).Scan(&d.ID, &d.Kind, &d.Status, &d.ConversationID, &unitRaw,
		&d.Question, &d.Answer, &sourceRaw, &d.DocID, &d.JobID, &d.CreatedAt)
	if err != nil {
		return d, err
	}
	d.UnitIDs = decodeSkillIDs(unitRaw)
	_ = json.Unmarshal(sourceRaw, &d.Sources)
	return d, nil
}

// kbDraftMarkdown 把草稿整理成一份可入库的文档。
// 头部明确标注「由历史问答生成、非原始文档」，避免日后被当成权威原文引用。
func kbDraftMarkdown(d kbDraft) string {
	var b strings.Builder
	title := d.Question
	if d.Kind == kbDraftKindNote {
		title = "主题笔记：" + truncateRunes(d.Question, 40)
	}
	fmt.Fprintf(&b, "# %s\n\n", truncateRunes(title, 60))
	b.WriteString("> 本文由历史问答整理生成，不是原始文档；采纳前请核对下方引用来源。\n")
	fmt.Fprintf(&b, "> 沉淀时间：%s · 类型：%s\n\n", d.CreatedAt.Format("2006-01-02 15:04"), d.Kind)
	if d.Kind == kbDraftKindQA {
		b.WriteString("## 问题\n\n" + d.Question + "\n\n")
	}
	b.WriteString("## 内容\n\n" + d.Answer + "\n\n")
	if len(d.Sources) > 0 {
		b.WriteString("## 引用来源\n\n")
		for i, src := range d.Sources {
			fmt.Fprintf(&b, "- [%d] %s（%s，片段 %d）\n", i+1, src.Filename, src.DocID, src.ChunkIndex)
		}
	}
	return b.String()
}

// acceptKnowledgeDraft 把草稿写成 Markdown 并复用现有索引队列入库。
// 切块、嵌入、向量身份、章节抽取全部走同一条管线，不另起一套索引逻辑。
func (s *Server) acceptKnowledgeDraft(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	draftID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || draftID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid draft id"})
		return
	}
	d, err := s.loadDraft(c, draftID)
	if err != nil {
		fail(c, err)
		return
	}
	if d.Status != "pending" {
		c.JSON(http.StatusConflict, gin.H{"error": "该草稿已处理，请刷新列表"})
		return
	}
	if len(d.UnitIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "草稿没有归属知识库，无法入库"})
		return
	}
	unit := d.UnitIDs[0]
	exists, err := s.unitExists(c, unit)
	if err != nil {
		fail(c, err)
		return
	}
	if !exists {
		c.JSON(http.StatusConflict, gin.H{"error": "原知识库已被删除，无法入库"})
		return
	}

	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	filename := fmt.Sprintf("draft-%d-%s.md", d.ID, d.Kind)
	dir := filepath.Join(env("UPLOAD_DIR", "./data/uploads"), filepath.Base(unit))
	if err := os.MkdirAll(dir, 0750); err != nil {
		fail(c, err)
		return
	}
	dst := filepath.Join(dir, jobID+"_"+filename)
	if err := os.WriteFile(dst, []byte(kbDraftMarkdown(d)), 0640); err != nil {
		fail(c, err)
		return
	}
	j := &Job{ID: jobID, UnitID: unit, DocType: kbDraftDocType, Filename: filename, Path: dst,
		Status: "queued", Phase: phasePending, Message: "等待解析", SourceURL: "kb-draft:" + fmt.Sprint(d.ID)}
	if err := s.persistJob(j); err != nil {
		_ = os.Remove(dst)
		fail(c, err)
		return
	}
	s.jobs.Store(jobID, j)
	tag, err := s.db.Exec(c, `UPDATE kb_drafts SET status='accepted', job_id=$2, decided_at=now()
		WHERE id=$1 AND status='pending'`, d.ID, jobID)
	if err != nil {
		fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "该草稿已被其他人处理"})
		return
	}
	s.wakeWorker()
	logging.App().Info("kb_draft_accepted", "request_id", c.GetString(requestIDKey),
		"draft_id", d.ID, "job_id", jobID, "unit_id", unit)
	c.JSON(http.StatusOK, gin.H{"accepted": true, "job_id": jobID, "unit_id": unit, "filename": filename})
}

func (s *Server) dismissKnowledgeDraft(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	draftID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || draftID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid draft id"})
		return
	}
	// 先读一次，让「草稿不存在」返回 404 而不是被 UPDATE 0 行误报成 409。
	if _, err := s.loadDraft(c, draftID); err != nil {
		fail(c, err)
		return
	}
	tag, err := s.db.Exec(c, `UPDATE kb_drafts SET status='dismissed', decided_at=now()
		WHERE id=$1 AND status='pending'`, draftID)
	if err != nil {
		fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "该草稿已处理，请刷新列表"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"dismissed": true, "id": draftID})
}

// distillKnowledgeDrafts 把零散的问答草稿聚成一篇主题笔记。
// 它只产生**新的草稿**，不自动入库；原问答标记 merged，避免同一内容被采纳两次。
func (s *Server) distillKnowledgeDrafts(ctx context.Context, auto bool) (int, error) {
	rows, err := s.db.Query(ctx, `SELECT id,question,answer,sources,unit_ids FROM kb_drafts
		WHERE status='pending' AND kind=$1 ORDER BY created_at DESC LIMIT $2`, kbDraftKindQA, kbDistillBatch)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type qaItem struct {
		id       int64
		question string
		answer   string
		sources  []kbDraftSource
		units    []string
	}
	items := []qaItem{}
	for rows.Next() {
		var it qaItem
		var sourceRaw, unitRaw []byte
		if err := rows.Scan(&it.id, &it.question, &it.answer, &sourceRaw, &unitRaw); err != nil {
			return 0, err
		}
		_ = json.Unmarshal(sourceRaw, &it.sources)
		it.units = decodeSkillIDs(unitRaw)
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(items) == 0 || (auto && len(items) < kbAutoDistillMin) {
		return 0, nil
	}

	var prompt strings.Builder
	prompt.WriteString("下面是一个知识库问答系统里最近产生的问答记录。请把它们整理成一篇**主题笔记**，供日后检索复用。\n")
	prompt.WriteString("要求：\n1. 只保留有复用价值的结论与约束，删掉寒暄、重复和过程性描述。\n")
	prompt.WriteString("2. 按主题分 2-4 个小节，每节一个 Markdown 二级标题；不要写成问答列表。\n")
	prompt.WriteString("3. 不要新增任何原文没有的数字、时间或责任主体；不确定的内容直接不写。\n")
	prompt.WriteString("4. 用简体中文，直接输出笔记正文，不要解释你做了什么。\n\n")
	for i, it := range items {
		fmt.Fprintf(&prompt, "【记录 %d】\n问：%s\n答：%s\n\n", i+1,
			truncateRunes(it.question, 200), truncateRunes(it.answer, 800))
	}
	note, err := s.complete(ctx,
		"你是知识整理助手。把零散的问答整理成结构化笔记，只压缩不改写事实。",
		prompt.String())
	if err != nil {
		return 0, err
	}
	note = strings.TrimSpace(note)
	if note == "" {
		return 0, fmt.Errorf("整理结果为空")
	}

	unitSet := map[string]bool{}
	sourceSet := map[string]kbDraftSource{}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.id)
		for _, u := range it.units {
			unitSet[u] = true
		}
		for _, src := range it.sources {
			sourceSet[src.Filename] = src
		}
	}
	units := []string{}
	for u := range unitSet {
		units = append(units, u)
	}
	sources := []kbDraftSource{}
	for _, src := range sourceSet {
		sources = append(sources, src)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var noteID int64
	title := fmt.Sprintf("由 %d 条历史问答整理（%s）", len(items), time.Now().Format("2006-01-02"))
	hash := fmt.Sprintf("note-%d", time.Now().UnixNano())
	if err := tx.QueryRow(ctx, `INSERT INTO kb_drafts(kind,status,unit_ids,question,answer,sources,content_hash)
		VALUES($1,'pending',$2,$3,$4,$5,$6) RETURNING id`,
		kbDraftKindNote, encodeSkillIDs(units), title, note, mustJSON(sources), hash).Scan(&noteID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE kb_drafts SET status='merged', merged_into=$1, decided_at=now()
		WHERE id = ANY($2) AND status='pending'`, noteID, ids); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	logging.App().Info("kb_draft_distilled", "note_id", noteID, "merged", len(ids), "auto", auto)
	return len(ids), nil
}

func (s *Server) distillDrafts(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	merged, err := s.distillKnowledgeDrafts(c, false)
	if err != nil {
		fail(c, err)
		return
	}
	if merged == 0 {
		c.JSON(http.StatusOK, gin.H{"merged": 0, "message": "当前没有可整理的待审核问答"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"merged": merged, "message": fmt.Sprintf("已把 %d 条问答整理成一篇主题笔记，请在列表中审核", merged)})
}

// startKnowledgeDistiller 每小时检查一次积累量，够一批才整理。
// 自动整理也只产草稿，不会自动进入知识库——入库始终需要人工采纳。
func (s *Server) startKnowledgeDistiller() {
	if s.db == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			merged, err := s.distillKnowledgeDrafts(ctx, true)
			cancel()
			if err != nil {
				logging.App().Warn("kb_distill_failed", "error", err)
				continue
			}
			if merged > 0 {
				logging.App().Info("kb_distill_completed", "merged", merged)
			}
		}
	}()
}
