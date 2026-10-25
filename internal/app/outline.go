package app

// 内容框架：从已入库的片段里抽出章节标题，形成可点击的目录。
//
// 为什么值得做：知识库页面原先只有「42 个文本块」这种计数，用户看不出
// 里面讲了什么。章节是唯一既贴近原文结构、又能一眼读懂的索引。
// 抽错了也能看见——存进 doc_outlines，带规则版本，随时可重抽。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

const (
	// 规则变化时改这里，已抽的文档会因版本不一致而重抽。
	outlineRuleVersion = "v5"
	outlineMaxEntries  = 300
	outlineTitleMax    = 40
)

var (
	outlineChapterRe  = regexp.MustCompile(`^第[一二三四五六七八九十百零〇0-9]+章[\s　]*(.*)$`)
	outlineArticleRe  = regexp.MustCompile(`^第[一二三四五六七八九十百零〇0-9]+条[\s　]*(.*)$`)
	outlineSectionRe  = regexp.MustCompile(`^[一二三四五六七八九十]+、\s*(.*)$`)
	outlineMarkdownRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	outlineNumberedRe = regexp.MustCompile(`^[0-9]{1,2}[.、]\s*(\S.*)$`)
)

type outlineEntry struct {
	Level      int    `json:"level"`
	Title      string `json:"title"`
	DocID      string `json:"doc_id"`
	DocName    string `json:"doc_name"`
	ChunkIndex int    `json:"chunk_index"`
}

// extractOutlineFromText 按行扫描一个片段，挑出可能是标题的行。
// level 只区分三级：章 > 条/序号 > 其他，够用来做缩进层级，不做过度设计。
func extractOutlineFromText(content, docID, docName string, chunkIndex int) []outlineEntry {
	out := make([]outlineEntry, 0, 8)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len([]rune(line)) > 120 {
			continue
		}
		level, title := 0, ""
		switch {
		case outlineChapterRe.MatchString(line):
			level, title = 1, outlineChapterRe.FindStringSubmatch(line)[1]
			if title == "" {
				title = line
			}
		case outlineArticleRe.MatchString(line):
			level, title = 2, outlineArticleRe.FindStringSubmatch(line)[1]
			// 条号后常直接跟正文，取到第一个分隔符为止。
			title = firstSegment(strings.TrimSpace(title))
			if title == "" {
				title = line
			} else {
				title = articleLabel(line) + " " + title
			}
		case outlineSectionRe.MatchString(line):
			level, title = 2, outlineSectionRe.FindStringSubmatch(line)[1]
		case outlineMarkdownRe.MatchString(line):
			m := outlineMarkdownRe.FindStringSubmatch(line)
			level, title = len(m[1]), m[2]
		}
		if level == 0 {
			continue
		}
		title = trimOutlineTitle(title)
		if title == "" {
			continue
		}
		// 评分表类文档会把「第一节(9分)」当成标题，这类条目对检索没有价值，
		// 留在目录里只会让用户以为知识库里有这些章节。
		if strings.HasPrefix(title, "节(") || strings.HasPrefix(title, "节（") {
			continue
		}
		out = append(out, outlineEntry{
			Level: level, Title: title,
			DocID: docID, DocName: docName, ChunkIndex: chunkIndex,
		})
	}
	return out
}

// articleLabel 取出「第二十六条」本身，让目录项带上条号，便于对照原文。
func articleLabel(line string) string {
	end := strings.IndexAny(line, "　 ")
	if end < 0 {
		return ""
	}
	return line[:end]
}

func firstSegment(s string) string {
	for _, sep := range []string{"　", " ", "。", "；", "，", "：", ":", ";"} {
		if i := strings.Index(s, sep); i > 0 {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

func trimOutlineTitle(s string) string {
	// 目录页常见「标题<TAB>页码」，页码不是标题的一部分。
	if i := strings.Index(s, "\t"); i > 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.Trim(s, "　 "))
	s = strings.TrimSuffix(s, "：")
	s = strings.TrimSuffix(s, ":")
	runes := []rune(s)
	if len(runes) > outlineTitleMax {
		runes = runes[:outlineTitleMax]
		s = strings.TrimSpace(string(runes)) + "…"
	}
	// 纯数字或单个字符的「标题」没有检索价值，丢掉。
	if len([]rune(s)) < 2 {
		return ""
	}
	return s
}

// looksLikeTOC 判断一个片段是不是目录页。
// Word 导出的目录带 "TOC \o" 域代码，纯文本导出的目录则是一整行「目 录」。
// 只认这两种强信号，避免正文里偶然出现「目录」二字就被误判。
func looksLikeTOC(content string) bool {
	head := content
	if runes := []rune(head); len(runes) > 1500 {
		head = string(runes[:1500])
	}
	if strings.Contains(head, "TOC \\o") || strings.Contains(head, "TOC \\h") {
		return true
	}
	for _, line := range strings.Split(head, "\n") {
		switch strings.TrimSpace(line) {
		case "目录", "目 录", "目　录", "目  录", "目　　录":
			return true
		}
	}
	return false
}

// trimFrontMatter 砍掉正文开始之前的前置条目。
//
// 制度汇编的目录往往跨好几个片段，只靠「这个片段像目录」判断会漏掉后半段。
// 更本质的判断是：正文的第一条一定是「第X章」或「第X条」，在它之前出现的
// 非章级条目都是封面、目录、审批页的残留，留着只会让目录里多出一批
// 指向第 0 块的假章节。
func trimFrontMatter(entries []outlineEntry) []outlineEntry {
	firstBody := -1
	for i, e := range entries {
		title := strings.TrimSpace(e.Title)
		if strings.HasPrefix(title, "第") && (strings.Contains(title, "章") || strings.Contains(title, "条")) {
			firstBody = i
			break
		}
	}
	// 没有条款结构的文档（表格、评审模板）不做裁剪，否则会把整份目录删空。
	if firstBody <= 0 {
		return entries
	}
	out := make([]outlineEntry, 0, len(entries))
	for i, e := range entries {
		if i < firstBody && e.Level > 1 {
			continue
		}
		out = append(out, e)
	}
	return out
}

// extractDocOutline 读该文档全部片段，抽取章节并落库。
// 覆盖写：重抽永远以最新规则为准，不做增量合并（合并会让旧规则残留）。
func (s *Server) extractDocOutline(ctx context.Context, docID, filename string) (int, error) {
	if s.db == nil || docID == "" {
		return 0, nil
	}
	rows, err := s.db.Query(ctx, `SELECT chunk_index, content FROM chunk_text
		WHERE doc_id=$1 ORDER BY chunk_index`, docID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	entries := make([]outlineEntry, 0, 32)
	seen := map[string]bool{}
	for rows.Next() {
		var index int
		var content string
		if err := rows.Scan(&index, &content); err != nil {
			return 0, err
		}
		// 目录页不是章节。制度汇编的第一块常常就是整册的目录，把里面的
		// 「一、XX制度」当成章节，会凭空造出几十个都指向第 0 块的假条目。
		// 只保留目录页里的「第X章」——那才是真正的章标题。
		tocPage := looksLikeTOC(content)
		for _, e := range extractOutlineFromText(content, docID, filename, index) {
			if tocPage && e.Level > 1 {
				continue
			}
			key := fmt.Sprintf("%s|%d", e.Title, e.Level)
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, e)
			if len(entries) >= outlineMaxEntries {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	entries = trimFrontMatter(entries)

	payload, err := json.Marshal(entries)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO doc_outlines(doc_id,filename,entries,rule_version,extracted_at)
		VALUES($1,$2,$3,$4,now())
		ON CONFLICT(doc_id) DO UPDATE SET filename=EXCLUDED.filename,
			entries=EXCLUDED.entries, rule_version=EXCLUDED.rule_version, extracted_at=now()`,
		docID, filename, payload, outlineRuleVersion); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// unitOutline 汇总一个知识库下所有文档的章节，按文档分组返回。
func (s *Server) unitOutline(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	unitID := c.Param("id")
	docIDs, err := s.docIDsForUnits(c, []string{unitID})
	if err != nil {
		fail(c, err)
		return
	}
	// origin=original：只要原始文档。对话页的「覆盖章节」是快捷提问，
	// 不该出现沉淀文档的章节标题；内容框架/目录树要看全量，所以由调用方选。
	if c.Query("origin") == "original" {
		docIDs, err = s.originalDocIDs(c, docIDs)
		if err != nil {
			fail(c, err)
			return
		}
	}
	if len(docIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"documents": []any{}, "total": 0})
		return
	}

	rows, err := s.db.Query(c, `SELECT doc_id,filename,entries,rule_version,extracted_at
		FROM doc_outlines WHERE doc_id = ANY($1) ORDER BY filename`, docIDs)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()

	docs := []gin.H{}
	total := 0
	for rows.Next() {
		var docID, filename, version string
		var raw []byte
		var at time.Time
		if err := rows.Scan(&docID, &filename, &raw, &version, &at); err != nil {
			fail(c, err)
			return
		}
		var entries []outlineEntry
		_ = json.Unmarshal(raw, &entries)
		total += len(entries)
		docs = append(docs, gin.H{
			"doc_id": docID, "filename": filename,
			"entries": entries, "rule_version": version, "extracted_at": at,
		})
	}
	missing := len(docIDs) - len(docs)
	c.JSON(http.StatusOK, gin.H{
		"documents": docs,
		"total":     total,
		"missing":   missing, // 尚未抽取的文档数，页面据此提示「提取框架」
	})
}

// extractUnitOutline 手动触发：给该库所有缺框架的文档抽一次。
func (s *Server) extractUnitOutline(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	unitID := c.Param("id")
	docIDs, err := s.docIDsForUnits(c, []string{unitID})
	if err != nil {
		fail(c, err)
		return
	}
	extracted, failed := 0, 0
	for _, docID := range docIDs {
		name := s.documentFilename(c, docID)
		n, err := s.extractDocOutline(c, docID, name)
		if err != nil {
			logging.App().Warn("outline_extract_failed", "doc_id", docID, "error", err)
			failed++
			continue
		}
		if n > 0 {
			extracted++
		}
	}
	c.JSON(http.StatusOK, gin.H{"extracted": extracted, "failed": failed, "documents": len(docIDs)})
}

func (s *Server) documentFilename(ctx context.Context, docID string) string {
	var name string
	_ = s.db.QueryRow(ctx, `SELECT filename FROM documents WHERE doc_id=$1`, docID).Scan(&name)
	return name
}

// backfillOutlines 在启动后台跑一次：给已入库但还没有框架的文档补抽。
// 规则版本变了也会重抽，否则改了规则老数据永远停在旧规则。
func (s *Server) backfillOutlines(ctx context.Context) {
	if s.db == nil {
		return
	}
	rows, err := s.db.Query(ctx, `SELECT d.doc_id, d.filename FROM documents d
		WHERE d.chunk_count > 0 AND (
			NOT EXISTS (SELECT 1 FROM doc_outlines o WHERE o.doc_id = d.doc_id)
			OR EXISTS (SELECT 1 FROM doc_outlines o WHERE o.doc_id = d.doc_id AND o.rule_version <> $1)
		)`, outlineRuleVersion)
	if err != nil {
		logging.App().Warn("outline_backfill_query_failed", "error", err)
		return
	}
	type doc struct{ id, name string }
	todo := []doc{}
	for rows.Next() {
		var d doc
		if err := rows.Scan(&d.id, &d.name); err == nil {
			todo = append(todo, d)
		}
	}
	rows.Close()

	done := 0
	for _, d := range todo {
		if n, err := s.extractDocOutline(ctx, d.id, d.name); err != nil {
			logging.App().Warn("outline_backfill_failed", "doc_id", d.id, "error", err)
		} else if n > 0 {
			done++
		}
	}
	if done > 0 {
		logging.App().Info("outline_backfilled", "documents", done)
	}
}
