package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/ledongthuc/pdf"
	"golang.org/x/text/unicode/norm"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/rag"
	"knowledge-base/internal/vectorstore"
)

// embeddingDimensions 转发到 vectorstore，保持全仓库单一事实源。
//
// 用函数而不是包级变量：包级变量在 init 阶段求值，早于 main 里的 .env 加载，
// 会把 EMBED_DIMENSIONS 静默读成默认值，造成「配置了却不起作用」。
func embeddingDimensions() int { return vectorstore.EmbeddingDim() }

// 切块尺寸的默认值。实际生效值来自 retrieval_settings（页面可改，DB 优先），
// 这里的常量只用于建表默认值和环境变量兜底。
const (
	defaultChunkSize     = 1800
	defaultChunkOverlap  = 240
	embedBatchSize       = 16
	defaultMaxUploadMB   = 50
	defaultIndexTimeoutM = 15
	maxUnitNameRunes     = 100
)

// errUnitNotFound marks an explicit unit_id that does not exist.
var errUnitNotFound = errors.New("unit not found")

var supportedExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true,
	".html": true, ".htm": true, ".csv": true, ".docx": true, ".pdf": true,
}

func maxUploadBytes() int64 {
	mb, err := strconv.Atoi(env("MAX_UPLOAD_MB", strconv.Itoa(defaultMaxUploadMB)))
	if err != nil || mb <= 0 {
		mb = defaultMaxUploadMB
	}
	return int64(mb) << 20
}

// ingestConfig 把上传与切块的真实限制交给前端，避免页面文案自己写死一份。
// 这类数字最容易腐烂：服务端改了配置，界面还在承诺旧值，用户按界面判断
// 就会被拒或误解成本。
func (s *Server) ingestConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"max_upload_mb": maxUploadBytes() >> 20,
		"chunk_size":    s.chunkSize(),
		"chunk_overlap": s.chunkOverlap(),
		"extensions":    supportedExtensionList(),
	})
}

// supportedExtensionList 返回去点的扩展名，按字母序，供前端直接展示。
func supportedExtensionList() []string {
	out := make([]string, 0, len(supportedExtensions))
	for ext := range supportedExtensions {
		out = append(out, strings.TrimPrefix(ext, "."))
	}
	sort.Strings(out)
	return out
}

func indexTimeout() time.Duration {
	minutes, err := strconv.Atoi(env("INDEX_TIMEOUT_MINUTES", strconv.Itoa(defaultIndexTimeoutM)))
	if err != nil || minutes <= 0 {
		minutes = defaultIndexTimeoutM
	}
	return time.Duration(minutes) * time.Minute
}

// validateUpload performs the checks that do not need database access. It
// returns the HTTP status to use when validation fails. Callers may provide an
// existing unit_id or a unit_name that is resolved (and created) server-side.
func validateUpload(unitID, unitName, docType, filename string, size, maxBytes int64) (int, error) {
	switch {
	case unitID == "" && unitName == "":
		return 400, fmt.Errorf("unit_id or unit_name is required")
	case len([]rune(unitName)) > maxUnitNameRunes:
		return 400, fmt.Errorf("unit_name exceeds %d characters", maxUnitNameRunes)
	case docType == "":
		return 400, fmt.Errorf("doc_type is required")
	case filename == "":
		return 400, fmt.Errorf("file is required")
	case size <= 0:
		return 400, fmt.Errorf("uploaded file is empty")
	case maxBytes > 0 && size > maxBytes:
		return 413, fmt.Errorf("file exceeds the %d MB upload limit", maxBytes>>20)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !supportedExtensions[ext] {
		return 415, fmt.Errorf("unsupported document type: %s", ext)
	}
	return 0, nil
}

func (s *Server) upload(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	// Bound the request body before multipart parsing so oversized uploads are
	// rejected without spooling the whole file to disk.
	limit := maxUploadBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit+(1<<20))
	f, h, e := c.Request.FormFile("file")
	if e != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(e, &tooLarge) {
			c.JSON(413, gin.H{"error": fmt.Sprintf("文件超过 %d MB 上传上限", limit>>20)})
			return
		}
		c.JSON(400, gin.H{"error": "file is required"})
		return
	}
	defer f.Close()
	unitID := strings.TrimSpace(c.PostForm("unit_id"))
	unitName := strings.TrimSpace(c.PostForm("unit_name"))
	typ := strings.TrimSpace(c.PostForm("doc_type"))
	filename := filepath.Base(h.Filename)
	if status, err := validateUpload(unitID, unitName, typ, filename, h.Size, limit); err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	unit, e := s.resolveUploadUnit(c, unitID, unitName)
	if e != nil {
		if errors.Is(e, errUnitNotFound) {
			c.JSON(404, gin.H{"error": e.Error()})
			return
		}
		fail(c, e)
		return
	}

	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	dir := filepath.Join(env("UPLOAD_DIR", "./data/uploads"), filepath.Base(unit))
	if e = os.MkdirAll(dir, 0750); e != nil {
		fail(c, e)
		return
	}
	dst := filepath.Join(dir, jobID+"_"+filename)
	out, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if e != nil {
		fail(c, e)
		return
	}
	if _, e = io.Copy(out, f); e != nil {
		out.Close()
		_ = os.Remove(dst)
		fail(c, e)
		return
	}
	if e = out.Close(); e != nil {
		_ = os.Remove(dst)
		fail(c, e)
		return
	}

	// 上传只负责落盘与入队：解析与索引是任务状态，失败可在任务列表里重试，
	// 不因为文件本身解析不了而拒收（扫描件、损坏文件都能先存下来）。
	j := &Job{ID: jobID, UnitID: unit, DocType: typ, Filename: filename, Path: dst,
		Status: "queued", Phase: phasePending, Message: "等待解析"}
	if err := s.persistJob(j); err != nil {
		_ = os.Remove(dst)
		fail(c, err)
		return
	}
	s.jobs.Store(jobID, j)
	logging.App().Info("index_job_queued",
		"request_id", c.GetString(requestIDKey),
		"job_id", jobID,
		"unit_id", unit,
		"filename", filename,
		"doc_type", typ,
		"bytes", h.Size,
	)
	// 任务落库后由队列 worker 领取，重启也能自动续跑。
	s.wakeWorker()
	c.JSON(202, j)
}

func (s *Server) runIndexJob(j *Job, path string) {
	lock := s.docLock(j.UnitID + "\x00" + j.Filename)
	lock.Lock()
	defer lock.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), indexTimeout())
	defer cancel()
	s.indexFile(ctx, j, path)
}

// resolveUploadUnit returns the unit_id for an upload. An explicit unit_id must
// exist; otherwise unit_name is matched by name and a new unit is created when
// no match is found.
func (s *Server) resolveUploadUnit(ctx context.Context, unitID, unitName string) (string, error) {
	if unitID != "" {
		exists, err := s.unitExists(ctx, unitID)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", fmt.Errorf("%w: %s", errUnitNotFound, unitID)
		}
		return unitID, nil
	}
	var existing string
	err := s.db.QueryRow(ctx, `SELECT unit_id FROM units WHERE name=$1 ORDER BY created_at LIMIT 1`, unitName).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	id := newUnitID()
	if _, err := s.db.Exec(ctx, `INSERT INTO units(unit_id,name) VALUES($1,$2)`, id, unitName); err != nil {
		return "", err
	}
	logging.App().Info("unit_created", "unit_id", id, "name", unitName, "source", "upload")
	return id, nil
}

func newUnitID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "unit-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("unit-%d", time.Now().UnixNano())
}

func (s *Server) unitExists(ctx context.Context, unitID string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("PG_DSN is not configured")
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM units WHERE unit_id=$1)`, unitID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Server) indexFile(ctx context.Context, j *Job, path string) {
	started := time.Now()
	view := j.snapshot()
	logger := logging.App().With(
		"job_id", view.ID,
		"unit_id", view.UnitID,
		"filename", view.Filename,
		"doc_type", view.DocType,
	)
	logger.Info("index_job_started", "source", filepath.Base(path))
	s.updateJobPhase(j, "running", phaseParse, 10, "解析文档")
	text, e := extract(path)
	if e != nil {
		s.failJob(j, e.Error())
		return
	}
	if strings.TrimSpace(text) == "" {
		s.failJob(j, "文档未提取到文本（扫描件需要 OCR，或文件内容为空）")
		return
	}

	// 文档身份来自内容：同样的正文在任何知识库下都是同一份文档。
	// 已索引过的内容直接复用，跳过重复的解析结果与向量化。
	docID := docIDForText(text)
	if existing, err := s.documentByID(ctx, docID); err == nil && existing.ChunkCount > 0 {
		// 复用旧向量之前先确认两件事，任一不满足都必须重建：
		//   1. 向量空间没变（换了嵌入模型还复用 = 新旧向量混算）
		//   2. 向量库里确实还有这份文档（重建索引清空过库时必须自愈）
		reusable, identityErr := s.documentIdentityMatches(ctx, docID)
		if identityErr != nil {
			logging.App().Warn("document_identity_check_failed", "doc_id", docID, "error", identityErr)
		}
		if reusable && s.vectors != nil {
			actual, countErr := s.vectors.Count(ctx, []string{docID})
			if countErr != nil {
				logging.App().Warn("document_vector_count_failed", "doc_id", docID, "error", countErr)
			} else if actual == 0 {
				// documents 说有索引、向量库却是空的：复用会得到一个
				// 「看起来完成、实际检索不到」的索引，比失败更难排查。
				logging.App().Warn("document_vectors_missing", "doc_id", docID, "declared", existing.ChunkCount)
				reusable = false
			}
		}
		if reusable {
			if err := s.linkDocumentUnit(ctx, docID, view.UnitID); err != nil {
				s.failJob(j, err.Error())
				return
			}
			if err := s.pruneReplacedDocuments(ctx, view.UnitID, view.Filename, docID); err != nil {
				logger.Warn("document_prune_failed", "doc_id", docID, "error", err)
			}
			j.setChunks(existing.ChunkCount)
			s.updateJobPhase(j, "done", phaseDone, 100, "文档已存在，复用已有索引")
			logger.Info("index_job_reused_document", "doc_id", docID, "chunks", existing.ChunkCount)
			return
		}
		// 身份不一致：落到下面重新切块与向量化，覆盖旧索引。
		logger.Warn("document_reindex_identity_changed", "doc_id", docID, "filename", view.Filename)
	}
	chunker := rag.FixedChunker{Size: s.chunkSize(), Overlap: s.chunkOverlap()}
	parsed := chunker.Chunk(text)
	if len(parsed) == 0 {
		s.failJob(j, "文档未提取到文本")
		return
	}
	chunks := make([]string, len(parsed))
	for i, chunk := range parsed {
		chunks[i] = chunk.Content
	}
	logger.Info("index_job_doc_resolved", "doc_id", docID)
	s.updateJobPhase(j, "running", phaseIndex, 25, "调用 BGE-M3 生成向量")
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[start:end]
		vecs, err := s.embedContext(ctx, batch)
		if err != nil {
			s.failJob(j, err.Error())
			return
		}
		if len(vecs) != len(batch) {
			s.failJob(j, fmt.Sprintf("embedding API returned %d vectors for %d chunks", len(vecs), len(batch)))
			return
		}
		out := make([]vectorstore.Chunk, 0, len(batch))
		for i, vec := range vecs {
			if len(vec) != embeddingDimensions() {
				s.failJob(j, fmt.Sprintf("embedding dimension is %d, expected %d", len(vec), embeddingDimensions()))
				return
			}
			out = append(out, vectorstore.Chunk{
				DocID: docID, DocType: view.DocType, Filename: view.Filename,
				Index: start + i, Content: batch[i], Embedding: vec,
			})
		}
		if err := s.vectors.Upsert(ctx, out); err != nil {
			s.failJob(j, err.Error())
			return
		}
		// 关键词镜像与向量同批写入，混合检索才有数据可用。
		if err := s.syncChunkText(ctx, docID, out); err != nil {
			logging.App().Warn("chunk_text_write_failed", "doc_id", docID, "error", err)
		}
		j.setChunks(end)
		s.updateJobPhase(j, "running", phaseIndex, 25+70*end/len(chunks), "写出向量索引")
	}
	// Remove chunks left over from a previous, longer version of this document.
	if err := s.vectors.DeleteFrom(ctx, docID, len(chunks)); err != nil {
		s.failJob(j, err.Error())
		return
	}
	if err := s.deleteChunkTextFrom(ctx, docID, len(chunks)); err != nil {
		logging.App().Warn("chunk_text_prune_failed", "doc_id", docID, "error", err)
	}
	if err := s.recordDocument(ctx, view, docID, path, text, len(chunks), chunker.Name()); err != nil {
		logger.Error("document_record_failed", "error", err)
		s.failJob(j, fmt.Sprintf("文档记录失败: %v", err))
		return
	}
	// 索引完成即抽章节：页面上的「内容框架」不必等用户手动点。
	if _, err := s.extractDocOutline(ctx, docID, view.Filename); err != nil {
		logging.App().Warn("outline_extract_failed", "doc_id", docID, "error", err)
	}
	s.updateJobPhase(j, "done", phaseDone, 100, "索引完成")
	logger.Info("index_job_completed",
		"chunks", len(chunks),
		"duration_ms", time.Since(started).Milliseconds(),
	)
}

// recordDocument 以内容哈希为身份写入文档，并把文档挂到当前知识库下。
// 文档可能被多个知识库共用，因此这里只更新文档自身的元数据。
//
// 同时登记「这份向量是用什么建的」：embedding_model + embedding_dim + chunker。
// 没有这三个字段，换模型或换切块策略后旧索引无法被识别，只能靠人记得重建。
func (s *Server) recordDocument(ctx context.Context, view jobView, docID, path, text string, chunkCount int, chunkerName string) error {
	cfg := s.modelConfigFor(ctx)
	_, err := s.db.Exec(ctx, `INSERT INTO documents(doc_id,filename,doc_type,job_id,chunk_count,content_hash,file_path,source_url,embedding_model,embedding_dim,chunker,indexed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
		ON CONFLICT(doc_id) DO UPDATE
		SET doc_type=EXCLUDED.doc_type,job_id=EXCLUDED.job_id,
			chunk_count=EXCLUDED.chunk_count,content_hash=EXCLUDED.content_hash,
			file_path=EXCLUDED.file_path,source_url=EXCLUDED.source_url,
			embedding_model=EXCLUDED.embedding_model,embedding_dim=EXCLUDED.embedding_dim,
			chunker=EXCLUDED.chunker,indexed_at=now()`,
		docID, view.Filename, view.DocType, view.ID, chunkCount, contentHash(text), path, view.SourceURL,
		cfg.EmbedModel, vectorstore.EmbeddingDim(), chunkerName)
	if err != nil {
		return err
	}
	if err := s.linkDocumentUnit(ctx, docID, view.UnitID); err != nil {
		return err
	}
	// 同名但内容变了（重新解析结果不同）时，清掉旧版本，避免列表出现两份同名文档。
	return s.pruneReplacedDocuments(ctx, view.UnitID, view.Filename, docID)
}

// documentChunks 返回某份文档已入库的分块内容，用于在页面上核对索引结果。
func (s *Server) documentChunks(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	if s.vectors == nil {
		c.JSON(503, gin.H{"error": "vector store is not configured"})
		return
	}
	unit := c.Param("id")
	name := filepath.Base(c.Param("filename"))
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		c.JSON(400, gin.H{"error": "filename is required"})
		return
	}
	limit := 10
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = min(n, 50)
		}
	}
	offset := 0
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}

	ref, err := s.documentRefByUnitFile(c, unit, name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
			return
		}
		fail(c, err)
		return
	}
	// 多取一条用于判断是否还有下一页，避免额外引入 count 接口。
	chunks, err := s.vectors.Chunks(c, ref.DocID, limit+1, offset)
	if err != nil {
		fail(c, err)
		return
	}
	hasMore := len(chunks) > limit
	if hasMore {
		chunks = chunks[:limit]
	}

	docType := ref.DocType
	total := ref.ChunkCount
	out := make([]gin.H, 0, len(chunks))
	for _, chunk := range chunks {
		if docType == "" {
			docType = chunk.DocType
		}
		out = append(out, gin.H{"index": chunk.Index, "content": chunk.Content})
	}
	c.JSON(200, gin.H{
		"unit_id": unit, "filename": name, "doc_type": docType,
		"chunk_count": total, "offset": offset, "limit": limit, "has_more": hasMore,
		"chunks": out,
	})
}

// documentSource returns readable text extracted from the uploaded source file.
// It deliberately uses text/plain so source HTML is displayed as text by clients.
func (s *Server) documentSource(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	unit := c.Param("id")
	name := filepath.Base(c.Param("filename"))
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		c.JSON(400, gin.H{"error": "filename is required"})
		return
	}
	var path string
	if err := s.db.QueryRow(c, `SELECT d.file_path FROM documents d
		JOIN document_units du ON du.doc_id = d.doc_id
		WHERE du.unit_id=$1 AND d.filename=$2 LIMIT 1`, unit, name).Scan(&path); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			logging.App().Warn("document_source_record_missing", "request_id", c.GetString(requestIDKey), "unit_id", unit, "filename", name)
			c.JSON(http.StatusNotFound, gin.H{"error": "document record not found; reload the document list or re-index the file"})
			return
		}
		fail(c, err)
		return
	}
	path, err := checkedUploadPath(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logging.App().Warn("document_source_file_missing", "request_id", c.GetString(requestIDKey), "unit_id", unit, "filename", name)
			c.JSON(http.StatusNotFound, gin.H{"error": "source file is missing from upload storage; re-upload the document"})
			return
		}
		fail(c, err)
		return
	}
	content, err := extract(path)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(content))
}

// documentFile serves the original document. Formats the UI can render (PDF,
// Office, text, images) are returned inline with a concrete content type; other
// formats stay downloads. Add ?download=1 to force a download.
func (s *Server) documentFile(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	unit := c.Param("id")
	name := filepath.Base(c.Param("filename"))
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		c.JSON(400, gin.H{"error": "filename is required"})
		return
	}
	var path string
	if err := s.db.QueryRow(c, `SELECT d.file_path FROM documents d
		JOIN document_units du ON du.doc_id = d.doc_id
		WHERE du.unit_id=$1 AND d.filename=$2 LIMIT 1`, unit, name).Scan(&path); err != nil {
		fail(c, err)
		return
	}
	path, err := checkedUploadPath(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.JSON(http.StatusNotFound, gin.H{"error": "source document not found"})
			return
		}
		fail(c, err)
		return
	}
	contentType, inline := previewContentType(name)
	disposition := "attachment"
	if inline && c.Query("download") != "1" {
		disposition = "inline"
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	c.Header("Content-Type", contentType)
	c.File(path)
}

// previewContentType maps a filename to a content type and reports whether the
// format can be rendered by the document preview. HTML is served as text/plain
// on purpose: rendering an uploaded HTML file inline would execute its scripts
// on the same origin.
func previewContentType(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf":
		return "application/pdf", true
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", true
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", true
	case ".xls":
		return "application/vnd.ms-excel", true
	case ".csv":
		return "text/csv; charset=utf-8", true
	case ".txt", ".md", ".markdown", ".json", ".html", ".htm":
		return "text/plain; charset=utf-8", true
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	}
	return "application/octet-stream", false
}

// checkedUploadPath prevents a stale or corrupted documents.file_path from
// exposing files outside UPLOAD_DIR. Resolve symlinks before the containment check.
func checkedUploadPath(path string) (string, error) {
	if path == "" {
		return "", os.ErrNotExist
	}
	root, err := filepath.Abs(env("UPLOAD_DIR", "./data/uploads"))
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	file, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source document path is outside upload directory")
	}
	return file, nil
}

// unitDocuments 返回知识库关联的文档列表（文档本身可能被多个知识库共用）。
func (s *Server) unitDocuments(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	rows, e := s.db.Query(c, `SELECT d.doc_id, d.filename, d.doc_type, d.chunk_count, d.job_id, d.indexed_at
		FROM documents d JOIN document_units du ON du.doc_id = d.doc_id
		WHERE du.unit_id=$1 ORDER BY d.indexed_at DESC`, c.Param("id"))
	if e != nil {
		fail(c, e)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var docID, filename, docType, jobID string
		var chunks int
		var at time.Time
		if e = rows.Scan(&docID, &filename, &docType, &chunks, &jobID, &at); e != nil {
			fail(c, e)
			return
		}
		out = append(out, gin.H{
			"doc_id": docID, "filename": filename, "doc_type": docType, "chunk_count": chunks,
			"job_id": jobID, "indexed_at": at,
		})
	}
	if e = rows.Err(); e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, out)
}

// deleteDocument removes all vectors and the tracked source file for one
// document, which is how renamed or withdrawn archives are cleaned up.
func (s *Server) deleteDocument(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	unit := c.Param("id")
	name := filepath.Base(c.Param("filename"))
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		c.JSON(400, gin.H{"error": "filename is required"})
		return
	}
	if s.vectors == nil {
		c.JSON(503, gin.H{"error": "vector store is not configured"})
		return
	}
	ref, err := s.documentRefByUnitFile(c, unit, name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "document not found"})
			return
		}
		fail(c, err)
		return
	}
	// 先解除与当前知识库的关联；仍被其他知识库引用时保留文档与向量。
	if err := s.unlinkDocumentUnit(c, ref.DocID, unit); err != nil {
		fail(c, err)
		return
	}
	// detach_only=1 表示「只解除绑定」，用于知识库详情页：
	// 那里管理的是绑定关系，不该因为某个库解绑就销毁全局文档、向量与源文件
	// （文档可能正要被另一个库接管，或用户只是想从本库移出）。
	if c.Query("detach_only") == "1" {
		logging.App().Info("document_detached",
			"request_id", c.GetString(requestIDKey),
			"unit_id", unit, "filename", name, "doc_id", ref.DocID,
		)
		c.JSON(200, gin.H{
			"unit_id": unit, "filename": name, "doc_id": ref.DocID,
			"deleted": true, "fully_removed": false, "detached_only": true,
		})
		return
	}
	removed, err := s.cleanupOrphanDocument(c, ref.DocID)
	if err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("document_deleted",
		"request_id", c.GetString(requestIDKey),
		"unit_id", unit,
		"filename", name,
		"doc_id", ref.DocID,
		"fully_removed", removed,
	)
	c.JSON(200, gin.H{
		"unit_id": unit, "filename": name, "doc_id": ref.DocID,
		"deleted": true, "fully_removed": removed,
	})
}

func extract(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md", ".markdown", ".json", ".html", ".htm", ".csv":
		return string(b), nil
	case ".docx":
		return docxText(b)
	case ".pdf":
		return pdfText(path)
	default:
		return "", fmt.Errorf("unsupported document type: %s", ext)
	}
}

func docxText(b []byte) (string, error) {
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		return "", e
	}
	for _, f := range z.File {
		if f.Name == "word/document.xml" {
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			defer r.Close()
			xmlBytes, e := io.ReadAll(r)
			if e != nil {
				return "", e
			}
			var out strings.Builder
			decoder := xml.NewDecoder(bytes.NewReader(xmlBytes))
			for {
				tok, err := decoder.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return "", fmt.Errorf("parse DOCX document XML: %w", err)
				}
				switch item := tok.(type) {
				case xml.StartElement:
					switch item.Name.Local {
					case "tab":
						out.WriteByte('\t')
					case "br", "cr":
						out.WriteByte('\n')
					}
				case xml.EndElement:
					if item.Name.Local == "p" && out.Len() > 0 {
						out.WriteByte('\n')
					}
				case xml.CharData:
					out.Write([]byte(item))
				}
			}
			return strings.TrimSpace(out.String()), nil
		}
	}
	return "", fmt.Errorf("docx has no word/document.xml")
}

// pdfText 提取 PDF 文本：优先用 pdftotext（中文与排版支持最好），
// 未安装 poppler 时回退到纯 Go 解析，避免把外部二进制变成硬依赖。
// PDFTOTEXT_BIN 可指定绝对路径，适配 systemd 下 PATH 不含 /opt/homebrew/bin 的情况。
func pdfText(path string) (string, error) {
	text, err := pdftotextExtract(path)
	if err == nil && strings.TrimSpace(text) != "" {
		return text, nil
	}
	if err != nil {
		logging.App().Debug("pdftotext_unavailable", "path", path, "error", err)
	}

	return goPDFText(path)
}

func pdftotextExtract(path string) (string, error) {
	out, err := exec.Command(env("PDFTOTEXT_BIN", "pdftotext"), "-layout", path, "-").Output()
	if err != nil {
		return "", fmt.Errorf("run pdftotext: %w", err)
	}
	return string(out), nil
}

// goPDFText 用纯 Go 库逐页提取文本。扫描件（无文本层）会得到空结果，
// 缺 ToUnicode 映射的嵌入字体会丢字，这两类都需要 OCR 或 pdftotext 才能处理。
func goPDFText(path string) (text string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("内置 PDF 解析异常：%v", recovered)
		}
	}()

	file, reader, err := pdf.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var out strings.Builder
	pages := reader.NumPage()
	for i := 1; i <= pages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		content, pageErr := page.GetPlainText(nil)
		if pageErr != nil {
			continue
		}
		out.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			out.WriteString("\n")
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("未提取到文本（共 %d 页，可能是扫描件，需要 OCR 后才能索引）", pages)
	}
	return normalizePDFText(out.String()), nil
}

// normalizePDFText 修正纯 Go 解析的两个常见问题：
//  1. 中文被映射到康熙部首等兼容码位（如 U+2F02），NFKC 归一化后才能正常检索与展示；
//  2. 逐字定位的排版会让每个汉字独占一行，把连续的单字行拼回同一行。
//
// pdftotext 的输出不走这里，避免破坏它已经处理好的排版。
func normalizePDFText(text string) string {
	text = norm.NFKC.String(text)
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	prevSingle := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			prevSingle = false
			continue
		}
		single := isSingleCJK(trimmed)
		if single && prevSingle && len(out) > 0 && out[len(out)-1] != "" {
			out[len(out)-1] += trimmed
			continue
		}
		out = append(out, trimmed)
		prevSingle = single
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// isSingleCJK 判断是否为单个中日韩文字。只有这种行才可能是排版打散的结果，
// 正常的中文短标题（如「第一章」）不会被误合并。
func isSingleCJK(s string) bool {
	runes := []rune(s)
	if len(runes) != 1 {
		return false
	}
	r := runes[0]
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}
