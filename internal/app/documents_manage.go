package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

// 文档库是跨知识库的全局视图：文档是独立实体，这里的列表回答
// 「我有哪些文档、分别被哪些知识库引用、索引成功还是失败」。

const documentListQuery = `SELECT d.doc_id, d.filename, d.doc_type, d.chunk_count, d.indexed_at,
	COALESCE(json_agg(json_build_object('unit_id', u.unit_id, 'name', u.name))
		FILTER (WHERE u.unit_id IS NOT NULL), '[]'::json) AS units
	FROM documents d
	LEFT JOIN document_units du ON du.doc_id = d.doc_id
	LEFT JOIN units u ON u.unit_id = du.unit_id
	WHERE ($1 = '' OR d.filename ILIKE '%' || $1 || '%')
		AND ($2 = '' OR EXISTS (SELECT 1 FROM document_units du2 WHERE du2.doc_id = d.doc_id AND du2.unit_id = $2))
	GROUP BY d.doc_id, d.filename, d.doc_type, d.chunk_count, d.indexed_at
	ORDER BY d.indexed_at DESC
	LIMIT $3 OFFSET $4`

// 失败任务同样属于「文档」：文件已收下、只是没索引成功，文档库要能看见并重试。
// 同名文件在同一知识库下已有成功文档时不再重复展示失败记录。
const documentFailedQuery = `SELECT DISTINCT ON (j.unit_id, j.filename)
	j.job_id, j.unit_id, j.filename, j.doc_type, j.phase, j.updated_at, u.name
	FROM index_jobs j
	JOIN units u ON u.unit_id = j.unit_id
	WHERE j.status = 'failed'
		AND ($1 = '' OR j.filename ILIKE '%' || $1 || '%')
		AND ($2 = '' OR j.unit_id = $2)
		AND NOT EXISTS (
			SELECT 1 FROM documents d
			JOIN document_units du ON du.doc_id = d.doc_id
			WHERE d.filename = j.filename AND du.unit_id = j.unit_id
		)
	ORDER BY j.unit_id, j.filename, j.updated_at DESC
	LIMIT $3`

const documentCountQuery = `SELECT count(*), COALESCE(sum(chunk_count), 0),
	count(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM document_units du WHERE du.doc_id = documents.doc_id))
	FROM documents
	WHERE ($1 = '' OR filename ILIKE '%' || $1 || '%')
		AND ($2 = '' OR EXISTS (SELECT 1 FROM document_units du2 WHERE du2.doc_id = documents.doc_id AND du2.unit_id = $2))`

// 失败总数单独统计：原来用「本次取到的失败条数」充当总数，会被页大小截断，
// 分页之后顶部那句「N 份需要处理」会随翻页变化，失去意义。
const documentFailedCountQuery = `SELECT count(*) FROM (
	SELECT DISTINCT j.unit_id, j.filename FROM index_jobs j
	WHERE j.status = 'failed'
		AND ($1 = '' OR j.filename ILIKE '%' || $1 || '%')
		AND ($2 = '' OR j.unit_id = $2)
		AND NOT EXISTS (
			SELECT 1 FROM documents d
			JOIN document_units du ON du.doc_id = d.doc_id
			WHERE d.filename = j.filename AND du.unit_id = j.unit_id)) t`

// documentList 返回文档库列表：已索引文档 + 索引失败的任务，统一按时间倒序。
func (s *Server) documentList(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	unitID := strings.TrimSpace(c.Query("unit_id"))
	limit := 50
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 200)
		}
	}
	offset := 0
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}

	// state 让「可用 / 需处理」的筛选在服务端完成。否则分页之后，
	// 前端只能筛当前页，看起来就像「失败文档消失了」。
	state := strings.TrimSpace(c.Query("state"))
	if state != "" && state != "indexed" && state != "failed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "state must be indexed or failed"})
		return
	}

	indexedTotal, chunks, orphans := 0, 0, 0
	if err := s.db.QueryRow(c, documentCountQuery, q, unitID).Scan(&indexedTotal, &chunks, &orphans); err != nil {
		fail(c, err)
		return
	}
	failedTotal := 0
	if err := s.db.QueryRow(c, documentFailedCountQuery, q, unitID).Scan(&failedTotal); err != nil {
		fail(c, err)
		return
	}

	// 两个来源各自多取一些，合并排序后再分页（文档库规模有限，这样比 UNION 更易读）。
	fetch := limit + offset
	items := []gin.H{}
	if state != "failed" {
		rows, err := s.db.Query(c, documentListQuery, q, unitID, fetch, 0)
		if err != nil {
			fail(c, err)
			return
		}
		for rows.Next() {
			var docID, filename, docType string
			var chunkCount int
			var indexedAt time.Time
			var unitsRaw []byte
			if err := rows.Scan(&docID, &filename, &docType, &chunkCount, &indexedAt, &unitsRaw); err != nil {
				rows.Close()
				fail(c, err)
				return
			}
			units := []gin.H{}
			_ = json.Unmarshal(unitsRaw, &units)
			items = append(items, gin.H{
				"state": "indexed", "doc_id": docID, "filename": filename, "doc_type": docType,
				"chunk_count": chunkCount, "indexed_at": indexedAt,
				"ref_count": len(units), "units": units,
			})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			fail(c, err)
			return
		}
	}

	if state != "indexed" {
		failedRows, err := s.db.Query(c, documentFailedQuery, q, unitID, fetch)
		if err != nil {
			fail(c, err)
			return
		}
		for failedRows.Next() {
			var jobID, failedUnit, filename, docType, phase, unitName string
			var updatedAt time.Time
			if err := failedRows.Scan(&jobID, &failedUnit, &filename, &docType, &phase, &updatedAt, &unitName); err != nil {
				failedRows.Close()
				fail(c, err)
				return
			}
			items = append(items, gin.H{
				"state": "failed", "job_id": jobID, "filename": filename, "doc_type": docType,
				"phase": phase, "chunk_count": 0, "indexed_at": updatedAt, "ref_count": 1,
				"units": []gin.H{{"unit_id": failedUnit, "name": unitName}},
			})
		}
		failedRows.Close()
		if err := failedRows.Err(); err != nil {
			fail(c, err)
			return
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		left, _ := items[i]["indexed_at"].(time.Time)
		right, _ := items[j]["indexed_at"].(time.Time)
		return left.After(right)
	})
	if offset > len(items) {
		items = []gin.H{}
	} else {
		items = items[offset:]
	}
	if len(items) > limit {
		items = items[:limit]
	}

	total := indexedTotal + failedTotal
	switch state {
	case "indexed":
		total = indexedTotal
	case "failed":
		total = failedTotal
	}

	c.JSON(http.StatusOK, gin.H{
		"count": len(items), "total": total, "failed": failedTotal,
		"chunks": chunks, "orphans": orphans, "limit": limit, "offset": offset,
		"state": state, "items": items,
	})
}

// documentDetail 返回单份文档的元数据与引用它的知识库。
func (s *Server) documentDetail(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	docID := strings.TrimSpace(c.Param("doc_id"))
	ref, err := s.documentByID(c, docID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
			return
		}
		fail(c, err)
		return
	}
	rows, err := s.db.Query(c, `SELECT u.unit_id, u.name FROM document_units du
		JOIN units u ON u.unit_id = du.unit_id WHERE du.doc_id=$1 ORDER BY u.name`, docID)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	units := []gin.H{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			fail(c, err)
			return
		}
		units = append(units, gin.H{"unit_id": id, "name": name})
	}
	c.JSON(http.StatusOK, gin.H{
		"doc_id": ref.DocID, "filename": ref.Filename, "doc_type": ref.DocType,
		"chunk_count": ref.ChunkCount, "job_id": ref.JobID, "file_path": ref.FilePath,
		"unit_count": len(units), "units": units,
	})
}

// deleteDocumentGlobal 彻底删除一份文档：解除全部知识库关联、删除向量与源文件。
// 与 /units/:id/documents/:filename 的区别是它不受知识库范围限制。
func (s *Server) deleteDocumentGlobal(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	docID := strings.TrimSpace(c.Param("doc_id"))
	ref, err := s.documentByID(c, docID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "文档不存在"})
			return
		}
		fail(c, err)
		return
	}
	// 先解除关联，再复用孤儿清理逻辑（删向量、源文件、记录）。
	if _, err := s.db.Exec(c, `DELETE FROM document_units WHERE doc_id=$1`, docID); err != nil {
		fail(c, err)
		return
	}
	if _, err := s.cleanupOrphanDocument(c, docID); err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("document_deleted_global",
		"request_id", c.GetString(requestIDKey), "doc_id", docID, "filename", ref.Filename)
	c.JSON(http.StatusOK, gin.H{"doc_id": docID, "filename": ref.Filename, "deleted": true})
}
