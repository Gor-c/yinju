package app

// 知识库目录：把「知识库 → 文档 → 章节 → 片段」四级关系一次取出来。
//
// 为什么单独做一个接口：前端的树需要「章节挂在哪份文档下」以及「这个章节
// 对应第几块」，而 /outline 只给章节、/documents 只给文件，拼起来要三个请求
// 还要在前端做关联。这里直接按文档组装好，顺便带上每个片段的首句，
// 让没有条款结构的文档也能在树里展开成「第 N 块」。

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

type treeChunk struct {
	Index   int    `json:"index"`
	Preview string `json:"preview"`
}

type treeDocument struct {
	DocID      string         `json:"doc_id"`
	Filename   string         `json:"filename"`
	DocType    string         `json:"doc_type"`
	ChunkCount int            `json:"chunk_count"`
	Entries    []outlineEntry `json:"entries"`
	Chunks     []treeChunk    `json:"chunks"`
}

func (s *Server) unitTree(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	unitID := c.Param("id")
	rows, err := s.db.Query(c, `SELECT d.doc_id, d.filename, d.doc_type, d.chunk_count,
			COALESCE(o.entries, '[]'::jsonb)
		FROM documents d
		JOIN document_units du ON du.doc_id = d.doc_id
		LEFT JOIN doc_outlines o ON o.doc_id = d.doc_id
		WHERE du.unit_id = $1
		ORDER BY d.indexed_at DESC`, unitID)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()

	docs := []treeDocument{}
	docIDs := []string{}
	for rows.Next() {
		var doc treeDocument
		var entryRaw []byte
		if err := rows.Scan(&doc.DocID, &doc.Filename, &doc.DocType, &doc.ChunkCount, &entryRaw); err != nil {
			fail(c, err)
			return
		}
		entries := []outlineEntry{}
		_ = json.Unmarshal(entryRaw, &entries)
		doc.Entries = entries
		doc.Chunks = []treeChunk{}
		docs = append(docs, doc)
		docIDs = append(docIDs, doc.DocID)
	}
	if err := rows.Err(); err != nil {
		fail(c, err)
		return
	}

	// 片段首句只用来在树里辨认节点，正文交给 /chunks 接口按需取。
	// 一次取回全部会让大树变慢，所以这里只留 140 字。
	if len(docIDs) > 0 {
		chunkRows, err := s.db.Query(c, `SELECT doc_id, chunk_index, content
			FROM chunk_text WHERE doc_id = ANY($1) ORDER BY doc_id, chunk_index`, docIDs)
		if err != nil {
			logging.App().Warn("unit_tree_chunks_failed", "error", err)
		} else {
			defer chunkRows.Close()
			byDoc := map[string]int{}
			for i := range docs {
				byDoc[docs[i].DocID] = i
			}
			for chunkRows.Next() {
				var docID string
				var index int
				var content string
				if err := chunkRows.Scan(&docID, &index, &content); err != nil {
					continue
				}
				pos, ok := byDoc[docID]
				if !ok {
					continue
				}
				docs[pos].Chunks = append(docs[pos].Chunks, treeChunk{
					Index:   index,
					Preview: compactPreview(content, 140),
				})
			}
		}
	}

	chapters := 0
	chunks := 0
	for _, d := range docs {
		chapters += len(d.Entries)
		chunks += len(d.Chunks)
	}
	c.JSON(http.StatusOK, gin.H{
		"unit_id":        unitID,
		"documents":      docs,
		"total_chapters": chapters,
		"total_chunks":   chunks,
	})
}

// compactPreview 把换行与连续空白压成单个空格，避免树里出现一行空白。
func compactPreview(text string, limit int) string {
	out := make([]rune, 0, limit)
	space := false
	for _, r := range text {
		if r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == '　' {
			space = true
			continue
		}
		if space && len(out) > 0 {
			out = append(out, ' ')
		}
		space = false
		if len(out) >= limit {
			break
		}
		out = append(out, r)
	}
	if len([]rune(text)) > limit {
		return string(out) + "…"
	}
	return string(out)
}
