package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/websearch"
)

type webImportInput struct {
	URL      string `json:"url"`
	DocType  string `json:"doc_type"`
	Filename string `json:"filename"`
}

// webImport archives a public web page into a project: it fetches the page,
// stores its readable text next to the uploads and queues the usual indexing
// job, so imported pages are searchable and previewable like any document.
func (s *Server) webImport(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	if s.vectors == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "vector store is not configured"})
		return
	}
	unit := strings.TrimSpace(c.Param("id"))
	if unit == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unit id is required"})
		return
	}
	// Fetching external pages is part of the web capability, so it follows the
	// same switch instead of silently working while search is disabled.
	if !s.webSearchConfigFor(c).Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "联网搜索未启用，请先在设置页开启"})
		return
	}
	var in webImportInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	var unitName string
	if err := s.db.QueryRow(c, `SELECT name FROM units WHERE unit_id=$1`, unit).Scan(&unitName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "project not found: " + unit})
			return
		}
		fail(c, err)
		return
	}

	page, err := websearch.FetchStandalone(c, strings.TrimSpace(in.URL), 0)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// The same page must not be archived twice under different names.
	// 文档与知识库是多对多（document_units），documents 上没有 unit_id 列。
	var existing string
	if err := s.db.QueryRow(c, `SELECT d.filename FROM documents d
		JOIN document_units du ON du.doc_id=d.doc_id
		WHERE du.unit_id=$1 AND d.source_url=$2 LIMIT 1`,
		unit, page.URL).Scan(&existing); err == nil {
		c.JSON(http.StatusOK, gin.H{"imported": false, "duplicate": true, "filename": existing})
		return
	}

	docType := strings.TrimSpace(in.DocType)
	if docType == "" {
		docType = "网页资料"
	}
	filename := s.importFilename(c, unit, in.Filename, page)
	dir := filepath.Join(env("UPLOAD_DIR", "./data/uploads"), filepath.Base(unit))
	if err := os.MkdirAll(dir, 0750); err != nil {
		fail(c, err)
		return
	}
	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	dst := filepath.Join(dir, jobID+"_"+filename)
	body := fmt.Sprintf("# %s\n来源：%s\n抓取时间：%s\n\n%s",
		page.Title, page.URL, time.Now().Format("2006-01-02 15:04"), page.Text)
	if err := os.WriteFile(dst, []byte(body), 0640); err != nil {
		fail(c, err)
		return
	}

	j := &Job{
		ID: jobID, UnitID: unit, DocType: docType, Filename: filename, Path: dst,
		Status: "queued", Phase: phasePending, Message: "等待解析", SourceURL: page.URL,
	}
	if err := s.persistJob(j); err != nil {
		_ = os.Remove(dst)
		fail(c, err)
		return
	}
	s.jobs.Store(jobID, j)
	logging.App().Info("web_import_queued",
		"request_id", c.GetString(requestIDKey),
		"job_id", jobID,
		"unit_id", unit,
		"url", page.URL,
		"filename", filename,
		"bytes", page.Bytes,
	)
	s.wakeWorker()
	c.JSON(http.StatusAccepted, j)
}

// importFilename derives a stable, unique document name for an imported page.
func (s *Server) importFilename(ctx context.Context, unit, requested string, page websearch.Page) string {
	base := websearch.Slugify(strings.TrimSpace(requested), "")
	if base == "" {
		base = websearch.Slugify(page.Title, "web-page")
	}
	base = strings.TrimSuffix(base, ".txt") + ".txt"
	var taken string
	if err := s.db.QueryRow(ctx, `SELECT d.filename FROM documents d
		JOIN document_units du ON du.doc_id=d.doc_id
		WHERE du.unit_id=$1 AND d.filename=$2 LIMIT 1`, unit, base).Scan(&taken); err == nil {
		base = strings.TrimSuffix(base, ".txt") + "-" + fmt.Sprintf("%d", time.Now().Unix()%100000) + ".txt"
	}
	return base
}
