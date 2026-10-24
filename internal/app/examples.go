package app

// 示例问题：从章节标题出题，并**先用当前参数检索一次**，答不上来的丢掉。
//
// 为什么必须验证：示例问题点下去答不出来，比没有示例更伤信任。用户会
// 认为「连你推荐的都答不上，这库是不是空的」。验证成本是几次检索，
// 换来的是「点了一定有东西」的确定性。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type exampleQuestion struct {
	Question  string `json:"question"`
	Source    string `json:"source"` // 来源标题，用于说明「为什么问这个」
	DocName   string `json:"doc_name"`
	Validated bool   `json:"validated"`
}

func (s *Server) unitExamples(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	limit := 4
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 8 {
		limit = v
	}
	unitID := c.Param("id")
	docIDs, err := s.docIDsForUnits(c, []string{unitID})
	if err != nil {
		fail(c, err)
		return
	}
	if len(docIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"questions": []any{}})
		return
	}

	candidates := s.exampleCandidates(c, docIDs, limit*3)
	// 顺序验证而不是并发：一次命中要调 embedding，并发容易撞上游限流。
	kept := make([]exampleQuestion, 0, limit)
	for _, item := range candidates {
		if !s.exampleHasEvidence(c, docIDs, item.Question) {
			continue
		}
		item.Validated = true
		kept = append(kept, item)
		if len(kept) >= limit {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"questions": kept})
}

// exampleCandidates 从章节标题与文档名生成候选问题。
// 优先用章级标题（制度/规定名），它比「第X条」更像人能问出口的问题。
func (s *Server) exampleCandidates(ctx context.Context, docIDs []string, want int) []exampleQuestion {
	rows, err := s.db.Query(ctx, `SELECT filename, entries FROM doc_outlines
		WHERE doc_id = ANY($1) ORDER BY array_position($1::text[], doc_id)`, docIDs)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type docOutline struct {
		name    string
		entries []outlineEntry
	}
	docs := []docOutline{}
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			continue
		}
		var entries []outlineEntry
		_ = json.Unmarshal(raw, &entries)
		docs = append(docs, docOutline{name: name, entries: entries})
	}

	out := make([]exampleQuestion, 0, want)
	seen := map[string]bool{}
	add := func(q, source, doc string) {
		if q == "" || seen[q] || len(out) >= want {
			return
		}
		seen[q] = true
		out = append(out, exampleQuestion{Question: q, Source: source, DocName: doc})
	}

	// 第一轮：章级标题（level 1），形如「工程质量管理制度（YH/QW-10-2026）」。
	for _, d := range docs {
		for _, e := range d.entries {
			if e.Level != 1 {
				continue
			}
			title := cleanExampleTitle(e.Title)
			if len([]rune(title)) < 4 {
				continue
			}
			add("「"+title+"」主要规定了什么？", e.Title, d.name)
			if len(out) >= want {
				return out
			}
		}
	}

	// 第二轮：文档名兜底，至少让用户能问「这份文件讲什么」。
	for _, d := range docs {
		name := strings.TrimSuffix(strings.TrimSuffix(d.name, ".docx"), ".pdf")
		name = strings.TrimSuffix(name, ".txt")
		if len([]rune(name)) < 4 {
			continue
		}
		add("《"+name+"》主要讲了什么？", d.name, d.name)
		if len(out) >= want {
			break
		}
	}
	return out
}

func cleanExampleTitle(s string) string {
	s = strings.TrimSpace(s)
	// 去掉末尾的编号括号内容，保留可读的名字。
	if i := strings.Index(s, "（"); i > 4 {
		s = s[:i]
	}
	if i := strings.Index(s, "("); i > 4 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// exampleHasEvidence 用当前参数真跑一次检索，确认这个问题能拿到依据。
// 这里刻意不写 retrieval_traces：验证行为不是用户提问，不该污染留痕。
func (s *Server) exampleHasEvidence(ctx context.Context, docIDs []string, question string) bool {
	pipeline, err := s.retrievalPipeline(ctx)
	if err != nil {
		return false
	}
	matches, err := pipeline.Search(ctx, docIDs, question, 3)
	if err != nil {
		return false
	}
	return len(matches) > 0
}
