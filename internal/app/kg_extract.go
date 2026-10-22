package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

const (
	kgExtractMaxChunksPerDoc = 5
	kgExtractMaxDocuments    = 5
	kgExtractChunkPageSize   = 20
	kgExtractConcurrency     = 3
	kgExtractChunkRunes      = 1200
	kgExtractCallTimeout     = 180 * time.Second
	kgExtractTimeout         = 20 * time.Minute
)

const kgSystemPrompt = `你是知识图谱抽取器。请从用户给出的文本中抽取实体和实体之间明确表达的关系。
只输出一个 JSON 对象，不要输出解释、Markdown 或代码围栏，结构如下：
{"entities":[{"name":"实体名","type":"类型","description":"一句话描述"}],
 "relationships":[{"source":"实体名","target":"实体名","keywords":"关系关键词","description":"关系描述","evidence":"支撑该关系的原文片段"}]}
要求：
1. 实体名保持原文写法，不要翻译、不要编造。
2. type 只能从 组织、人员、地点、事件、制度、设备、风险、指标、文档、其他 中选择。
3. 关系必须能在文本中直接找到依据，不要推断；evidence 必须是原文中连续出现的一句话，不要改写。
4. 没有可抽取内容时返回 {"entities":[],"relationships":[]}。`

type kgEntity struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type kgRelation struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Keywords    string `json:"keywords"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}

type kgExtraction struct {
	Entities      []kgEntity   `json:"entities"`
	Relationships []kgRelation `json:"relationships"`
}

var (
	kgThinkPattern = regexp.MustCompile(`(?s) thinking.*?<｜end▁of▁thinking｜>`)
	kgSpacePattern = regexp.MustCompile(`[ \t]+`)
)

func kgHash(parts ...string) string {
	sum := md5.Sum([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func normalizeKGName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'“”‘’`")
	value = kgSpacePattern.ReplaceAllString(value, " ")
	// 中文之间不要空格，便于同名实体归并。
	value = strings.ReplaceAll(value, " ", "")
	runes := []rune(value)
	if len(runes) > 60 {
		value = string(runes[:60])
	}
	return value
}

func parseKGJSON(raw string) (kgExtraction, error) {
	text := kgThinkPattern.ReplaceAllString(raw, "")
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return kgExtraction{}, fmt.Errorf("模型未返回 JSON")
	}
	var out kgExtraction
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return kgExtraction{}, err
	}
	return out, nil
}

// kgHTTP has no global timeout: extraction is bounded per call by context so
// slow local models are not cut off at the default 90s used by chat.
var kgHTTP = &http.Client{}

func (s *Server) kgComplete(ctx context.Context, messages []gin.H) (string, error) {
	cfg := s.modelConfigFor(ctx)
	base := strings.TrimRight(cfg.LLMBaseURL, "/")
	key := cfg.LLMAPIKey
	if base == "" || missingModelCredential(base, key) {
		return "", errChatConfig
	}
	body, _ := json.Marshal(gin.H{
		"model":           cfg.LLMModel,
		"messages":        messages,
		"temperature":     0.1,
		"response_format": gin.H{"type": "json_object"},
	})

	var lastErr error
	for attempt := 1; attempt <= maxModelAttempts; attempt++ {
		content, err := s.kgCompleteOnce(ctx, base, key, body)
		if err == nil {
			return content, nil
		}
		lastErr = err
		if attempt == maxModelAttempts || !retryableUpstream(err) {
			break
		}
		if !waitModelRetry(ctx, attempt) {
			break
		}
	}
	return "", lastErr
}

func (s *Server) kgCompleteOnce(ctx context.Context, base, key string, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := kgHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", &upstreamError{Kind: "chat", Status: res.StatusCode, Body: truncateRunes(string(raw), 300)}
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("chat API returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

func (s *Server) extractChunkKG(ctx context.Context, content string) (kgExtraction, error) {
	callCtx, cancel := context.WithTimeout(ctx, kgExtractCallTimeout)
	defer cancel()
	messages := []gin.H{
		{"role": "system", "content": kgSystemPrompt},
		{"role": "user", "content": "文本：\n" + truncateRunes(content, kgExtractChunkRunes)},
	}
	raw, err := s.kgComplete(callCtx, messages)
	if err != nil {
		return kgExtraction{}, err
	}
	return parseKGJSON(raw)
}

// normalizeEvidence 去掉空白，降低模型输出与原文在换行/空格上的差异。
func normalizeEvidence(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// relationEvidenceValid 要求模型给出能在原文中连续命中的证据句。
func relationEvidenceValid(chunkText, evidence string) bool {
	ev := normalizeEvidence(evidence)
	if len([]rune(ev)) < 6 {
		return false
	}
	return strings.Contains(normalizeEvidence(chunkText), ev)
}

func (s *Server) saveKGChunk(ctx context.Context, unitID, chunkKey, chunkText string, result kgExtraction) (int, int, error) {
	sourceJSON, _ := json.Marshal([]string{chunkKey})
	entities, relations := 0, 0
	for _, item := range result.Entities {
		name := normalizeKGName(item.Name)
		if len([]rune(name)) < 2 {
			continue
		}
		entityType := strings.TrimSpace(item.Type)
		if entityType == "" {
			entityType = "其他"
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO kg_entities(unit_id,entity_id,name,entity_type,description,source_ids,weight)
			VALUES($1,$2,$3,$4,$5,$6,1)
			ON CONFLICT (unit_id,entity_id) DO UPDATE SET
				description=CASE WHEN length(excluded.description)>length(kg_entities.description) THEN excluded.description ELSE kg_entities.description END,
				entity_type=CASE WHEN excluded.entity_type<>'' THEN excluded.entity_type ELSE kg_entities.entity_type END,
				source_ids=(SELECT COALESCE(jsonb_agg(DISTINCT value),'[]'::jsonb) FROM jsonb_array_elements(kg_entities.source_ids || excluded.source_ids) AS value),
				weight=(SELECT count(DISTINCT value) FROM jsonb_array_elements(kg_entities.source_ids || excluded.source_ids) AS value),
				updated_at=now()`,
			unitID, kgHash(unitID, name), name, entityType, strings.TrimSpace(item.Description), json.RawMessage(sourceJSON)); err != nil {
			return entities, relations, err
		}
		entities++
	}
	rejected := 0
	for _, item := range result.Relationships {
		// 没有可核对的原文证据时直接丢弃，避免模型凭空推断。
		if !relationEvidenceValid(chunkText, item.Evidence) {
			rejected++
			continue
		}
		src := normalizeKGName(item.Source)
		tgt := normalizeKGName(item.Target)
		if len([]rune(src)) < 2 || len([]rune(tgt)) < 2 || src == tgt {
			continue
		}
		srcID := kgHash(unitID, src)
		tgtID := kgHash(unitID, tgt)
		if srcID > tgtID {
			srcID, tgtID = tgtID, srcID
			src, tgt = tgt, src
		}
		// 端点实体不存在时补建，避免边悬空。
		for _, pair := range [][2]string{{srcID, src}, {tgtID, tgt}} {
			if _, err := s.db.Exec(ctx, `INSERT INTO kg_entities(unit_id,entity_id,name,entity_type,description,source_ids,weight)
				VALUES($1,$2,$3,'其他','',$4,0) ON CONFLICT (unit_id,entity_id) DO NOTHING`,
				unitID, pair[0], pair[1], json.RawMessage(sourceJSON)); err != nil {
				return entities, relations, err
			}
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO kg_relations(unit_id,relation_id,src_id,tgt_id,keywords,description,evidence,source_ids,weight)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,1)
			ON CONFLICT (unit_id,relation_id) DO UPDATE SET
				description=CASE WHEN length(excluded.description)>length(kg_relations.description) THEN excluded.description ELSE kg_relations.description END,
				keywords=CASE WHEN excluded.keywords<>'' THEN excluded.keywords ELSE kg_relations.keywords END,
				evidence=CASE WHEN length(excluded.evidence)>length(kg_relations.evidence) THEN excluded.evidence ELSE kg_relations.evidence END,
				source_ids=(SELECT COALESCE(jsonb_agg(DISTINCT value),'[]'::jsonb) FROM jsonb_array_elements(kg_relations.source_ids || excluded.source_ids) AS value),
				weight=(SELECT count(DISTINCT value) FROM jsonb_array_elements(kg_relations.source_ids || excluded.source_ids) AS value),
				updated_at=now()`,
			unitID, kgHash(unitID, srcID, tgtID), srcID, tgtID,
			strings.TrimSpace(item.Keywords), strings.TrimSpace(item.Description), strings.TrimSpace(item.Evidence),
			json.RawMessage(sourceJSON)); err != nil {
			return entities, relations, err
		}
		relations++
	}
	if rejected > 0 {
		logging.App().Info("kg_relations_rejected", "unit_id", unitID, "chunk", chunkKey, "count", rejected)
	}
	return entities, relations, nil
}

type kgChunk struct {
	key     string
	content string
}

func (s *Server) collectKGChunks(ctx context.Context, unitID string) ([]kgChunk, error) {
	rows, err := s.db.Query(ctx, `SELECT d.doc_id FROM documents d
		JOIN document_units du ON du.doc_id = d.doc_id
		WHERE du.unit_id=$1 ORDER BY d.indexed_at DESC LIMIT $2`,
		unitID, kgExtractMaxDocuments)
	if err != nil {
		return nil, err
	}
	docIDs := []string{}
	for rows.Next() {
		var docID string
		if err = rows.Scan(&docID); err != nil {
			rows.Close()
			return nil, err
		}
		docIDs = append(docIDs, docID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}

	out := []kgChunk{}
	for _, docID := range docIDs {
		for offset := 0; offset < kgExtractMaxChunksPerDoc; offset += kgExtractChunkPageSize {
			size := kgExtractChunkPageSize
			if remain := kgExtractMaxChunksPerDoc - offset; remain < size {
				size = remain
			}
			items, err := s.vectors.Chunks(ctx, docID, size, offset)
			if err != nil || len(items) == 0 {
				break
			}
			for _, item := range items {
				if strings.TrimSpace(item.Content) == "" {
					continue
				}
				out = append(out, kgChunk{
					key:     fmt.Sprintf("%s:%d", docID, item.Index),
					content: item.Content,
				})
			}
		}
	}
	return out, nil
}

func (s *Server) runKGExtraction(unitID string) {
	ctx, cancel := context.WithTimeout(context.Background(), kgExtractTimeout)
	defer cancel()

	fail := func(err error) {
		_, _ = s.db.Exec(context.Background(), `UPDATE kg_jobs SET status='failed',error=$2,updated_at=now() WHERE unit_id=$1`, unitID, err.Error())
		logging.App().Error("kg_extract_failed", "unit_id", unitID, "error", err)
	}
	if s.vectors == nil {
		fail(fmt.Errorf("vector store is not configured"))
		return
	}
	chunks, err := s.collectKGChunks(ctx, unitID)
	if err != nil {
		fail(err)
		return
	}
	if _, err = s.db.Exec(ctx, `UPDATE kg_jobs SET chunks_total=$2,updated_at=now() WHERE unit_id=$1`, unitID, len(chunks)); err != nil {
		fail(err)
		return
	}

	var (
		mu     sync.Mutex
		done   int
		failed int
		wg     sync.WaitGroup
		sem    = make(chan struct{}, kgExtractConcurrency)
	)
	for _, chunk := range chunks {
		chunk := chunk
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			result, extractErr := s.extractChunkKG(ctx, chunk.content)
			if extractErr != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				logging.App().Warn("kg_extract_chunk_failed", "unit_id", unitID, "chunk", chunk.key, "error", extractErr)
			} else if _, _, saveErr := s.saveKGChunk(ctx, unitID, chunk.key, truncateRunes(chunk.content, kgExtractChunkRunes), result); saveErr != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				logging.App().Warn("kg_save_chunk_failed", "unit_id", unitID, "chunk", chunk.key, "error", saveErr)
			}
			mu.Lock()
			done++
			current := done
			mu.Unlock()
			_, _ = s.db.Exec(ctx, `UPDATE kg_jobs SET chunks_done=$2,updated_at=now() WHERE unit_id=$1`, unitID, current)
		}()
	}
	wg.Wait()

	var entities, relations int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM kg_entities WHERE unit_id=$1`, unitID).Scan(&entities)
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM kg_relations WHERE unit_id=$1`, unitID).Scan(&relations)
	status := "done"
	errorText := ""
	if failed > 0 {
		errorText = fmt.Sprintf("%d 个片段抽取失败", failed)
	}
	if failed == len(chunks) && len(chunks) > 0 {
		status = "failed"
	}
	_, _ = s.db.Exec(ctx, `UPDATE kg_jobs SET status=$2,entities=$3,relations=$4,error=$5,updated_at=now() WHERE unit_id=$1`,
		unitID, status, entities, relations, errorText)
	logging.App().Info("kg_extract_completed", "unit_id", unitID, "chunks", len(chunks), "entities", entities, "relations", relations, "failed", failed)
}

func (s *Server) startKGExtraction(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id := c.Param("id")
	var exists bool
	if err := s.db.QueryRow(c, `SELECT EXISTS(SELECT 1 FROM units WHERE unit_id=$1)`, id).Scan(&exists); err != nil {
		fail(c, err)
		return
	}
	if !exists {
		c.JSON(404, gin.H{"error": "unit not found: " + id})
		return
	}
	var status string
	_ = s.db.QueryRow(c, `SELECT status FROM kg_jobs WHERE unit_id=$1`, id).Scan(&status)
	if status == "running" {
		c.JSON(409, gin.H{"error": "该项目的图谱正在构建中"})
		return
	}
	if _, err := s.db.Exec(c, `INSERT INTO kg_jobs(unit_id,status,chunks_total,chunks_done,entities,relations,error,updated_at)
		VALUES($1,'running',0,0,0,0,'',now())
		ON CONFLICT (unit_id) DO UPDATE SET status='running',chunks_total=0,chunks_done=0,entities=0,relations=0,error='',updated_at=now()`, id); err != nil {
		fail(c, err)
		return
	}
	go s.runKGExtraction(id)
	c.JSON(202, gin.H{"status": "running"})
}

func (s *Server) kgStatus(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id := c.Param("id")
	var status string
	var total, done, entities, relations int
	var errorText string
	var updatedAt time.Time
	err := s.db.QueryRow(c, `SELECT status,chunks_total,chunks_done,entities,relations,error,updated_at
		FROM kg_jobs WHERE unit_id=$1`, id).Scan(&status, &total, &done, &entities, &relations, &errorText, &updatedAt)
	if err != nil {
		c.JSON(200, gin.H{"status": "idle", "chunks_total": 0, "chunks_done": 0, "entities": 0, "relations": 0, "error": ""})
		return
	}
	c.JSON(200, gin.H{
		"status": status, "chunks_total": total, "chunks_done": done,
		"entities": entities, "relations": relations, "error": errorText, "updated_at": updatedAt,
	})
}

// entityGraph returns the extracted entity graph for a project.
func (s *Server) entityGraph(c *gin.Context, unitID string) {
	nodes := []graphNode{}
	edges := []graphEdge{}

	// 可按 document 维度查看：实体的 source_ids 记录 chunkKey（新格式 doc-xxx:index，
	// 旧格式 unit:filename:index），用两种前缀匹配，历史数据也能过滤。
	docID := strings.TrimSpace(c.Query("doc_id"))
	docName := ""
	if docID != "" {
		_ = s.db.QueryRow(c, `SELECT filename FROM documents WHERE doc_id=$1`, docID).Scan(&docName)
	}

	entityQuery := `SELECT entity_id,name,entity_type,description,weight FROM kg_entities WHERE unit_id=$1`
	entityArgs := []any{unitID}
	relationQuery := `SELECT src_id,tgt_id,keywords,weight,evidence FROM kg_relations WHERE unit_id=$1`
	relationArgs := []any{unitID}
	if docID != "" {
		const docFilter = ` AND (EXISTS (SELECT 1 FROM jsonb_array_elements_text(source_ids) s WHERE s LIKE $2 || ':%')
			OR ($3 <> '' AND EXISTS (SELECT 1 FROM jsonb_array_elements_text(source_ids) s WHERE s LIKE '%:' || $3 || ':%')))`
		entityQuery += docFilter
		relationQuery += docFilter
		entityArgs = append(entityArgs, docID, docName)
		relationArgs = append(relationArgs, docID, docName)
	}
	entityQuery += ` ORDER BY weight DESC LIMIT 300`
	relationQuery += ` ORDER BY weight DESC LIMIT 500`

	rows, err := s.db.Query(c, entityQuery, entityArgs...)
	if err != nil {
		fail(c, err)
		return
	}
	type entityInfo struct {
		ID          string `json:"entity_id"`
		Type        string `json:"entity_type"`
		Description string `json:"description"`
		Weight      int    `json:"weight"`
	}
	meta := map[string]entityInfo{}
	for rows.Next() {
		var id, name, entityType, description string
		var weight int
		if err = rows.Scan(&id, &name, &entityType, &description, &weight); err != nil {
			rows.Close()
			fail(c, err)
			return
		}
		nodeID := "entity:" + id
		nodes = append(nodes, graphNode{ID: nodeID, Label: name, Type: "entity", Weight: weight, Kind: entityType, Detail: description})
		meta[nodeID] = entityInfo{ID: id, Type: entityType, Description: description, Weight: weight}
	}
	rows.Close()

	relRows, err := s.db.Query(c, relationQuery, relationArgs...)
	if err != nil {
		fail(c, err)
		return
	}
	defer relRows.Close()
	for relRows.Next() {
		var src, tgt, keywords, evidence string
		var weight int
		if err = relRows.Scan(&src, &tgt, &keywords, &weight, &evidence); err != nil {
			fail(c, err)
			return
		}
		source := "entity:" + src
		target := "entity:" + tgt
		if _, ok := meta[source]; !ok {
			continue
		}
		if _, ok := meta[target]; !ok {
			continue
		}
		edges = append(edges, graphEdge{Source: source, Target: target, Type: "relation", Label: keywords, Weight: weight, Evidence: evidence})
	}
	if err = relRows.Err(); err != nil {
		fail(c, err)
		return
	}

	c.JSON(200, gin.H{
		"unit_id":  unitID,
		"source":   "entity",
		"doc_id":   docID,
		"doc_name": docName,
		"nodes":    nodes,
		"edges":    edges,
		"stats": gin.H{
			"nodes":     len(nodes),
			"edges":     len(edges),
			"entities":  len(meta),
			"relations": len(edges),
			"local":     true,
		},
	})
}
