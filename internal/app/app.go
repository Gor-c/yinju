package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/vectorstore"
	"knowledge-base/internal/websearch"
)

type Server struct {
	db      *pgxpool.Pool
	vectors vectorstore.Store
	jobs    sync.Map
	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
	http    *http.Client
	engine  *gin.Engine

	// models 缓存当前生效的模型配置（数据库优先，环境变量兜底）。
	modelsMu sync.RWMutex
	models   *modelConfig
	modelsAt time.Time

	// workerWake 唤醒队列消费者，避免上传后等待下一次轮询。
	workerWake chan struct{}

	// web 为内置 Agent 提供联网搜索；未配置时为 nil，工具不注册。
	// 生产环境由 web_search_settings 表驱动，测试中可直接注入。
	web *websearch.Client

	webMu     sync.RWMutex
	webCfg    webSearchConfig
	webAt     time.Time
	webClient *websearch.Client
}

// serverStartedAt identifies this process in 404 responses, so a caller hitting
// an old binary can tell it apart from the freshly built one.
var serverStartedAt = time.Now().UTC().Format(time.RFC3339)

func New() (*Server, error) {
	s := &Server{http: &http.Client{Timeout: 90 * time.Second}}
	// Web search is opt-in: without configuration the tool stays unregistered.
	web, err := websearch.FromEnv()
	if err != nil {
		return nil, err
	}
	s.web = web
	if dsn := os.Getenv("PG_DSN"); dsn != "" {
		p, e := pgxpool.New(context.Background(), dsn)
		if e != nil {
			return nil, e
		}
		store, err := vectorstore.New(env("VECTOR_STORE", "pgvector"), p)
		if err != nil {
			p.Close()
			return nil, err
		}
		s.db = p
		s.vectors = store
		s.workerWake = make(chan struct{}, 1)
		// 多来源模型配置表：老库首次启动时自动补齐，失败只告警（无 DDL 权限时仍可跑）。
		s.ensureModelProviderSchema(context.Background())
		// 文档中心模型：documents 以 doc_id 为主键，document_units 记录多对多关系。
		s.ensureDocumentSchema(context.Background())
		// 任务阶段列（上传 → 解析 → 索引）。
		s.ensureJobSchema(context.Background())
		// 运行详情展示触发本次运行的用户问题。
		s.ensureAgentRunSchema(context.Background())
		// 持久化队列消费者：启动时重新排队被中断的任务，之后持续领取 queued 任务。
		s.startJobWorker()
		// 图谱抽取在进程内运行，重启后把遗留的 running 标记为中断。
		_, _ = p.Exec(context.Background(), `UPDATE kg_jobs SET status='failed', error='服务重启中断', updated_at=now() WHERE status='running'`)
	}
	s.engine = s.routes()
	return s, nil
}

func (s *Server) routes() *gin.Engine {
	// Route diagnostics and internal gin errors follow the same logging rules.
	gin.DefaultWriter = ginLogWriter{logger: logging.App(), level: slog.LevelDebug, message: "gin_router"}
	gin.DefaultErrorWriter = ginLogWriter{logger: logging.App(), level: slog.LevelError, message: "gin_error"}

	r := gin.New()
	r.Use(s.requestIDMiddleware, s.accessLogMiddleware, s.recoveryMiddleware, s.authMiddleware())
	r.Static("/assets", filepath.Join("frontend", "dist", "assets"))
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok", "database": s.db != nil}) })
	r.POST("/mcp", s.mcp)
	r.GET("/mcp", func(c *gin.Context) { c.Header("Allow", "POST"); c.Status(http.StatusMethodNotAllowed) })
	a := r.Group("/api/v1")
	a.GET("/units", s.units)
	// 文档库：跨知识库的文档视图。
	a.GET("/documents", s.documentList)
	a.GET("/documents/:doc_id", s.documentDetail)
	a.DELETE("/documents/:doc_id", s.deleteDocumentGlobal)
	a.GET("/units/meta", s.unitMeta)
	a.GET("/units/profiles", s.unitProfiles)
	a.POST("/units", s.createUnit)
	a.PATCH("/units/:id", s.updateUnit)
	a.GET("/units/:id/agent-config", s.unitAgentConfig)
	a.GET("/units/:id/graph", s.unitGraph)
	a.POST("/units/:id/kg/extract", s.startKGExtraction)
	a.GET("/units/:id/kg/status", s.kgStatus)
	a.GET("/units/:id/profile", s.profile)
	a.GET("/units/:id/hazards", s.hazards)
	a.GET("/units/:id/audits", s.audits)
	a.GET("/units/:id/documents", s.unitDocuments)
	a.GET("/units/:id/documents/:filename/chunks", s.documentChunks)
	a.GET("/units/:id/documents/:filename/source", s.documentSource)
	a.GET("/units/:id/documents/:filename/file", s.documentFile)
	a.DELETE("/units/:id/documents/:filename", s.deleteDocument)
	a.POST("/upload", s.upload)
	a.GET("/jobs", s.jobList)
	a.GET("/jobs/:id", s.job)
	a.POST("/jobs/:id/retry", s.retryJob)
	a.POST("/ask", s.ask)
	a.GET("/agent/config", s.agentConfig)
	a.GET("/agent/definitions", s.agentDefinitions)
	a.POST("/agent/skills", s.uploadSkillDef)
	a.POST("/agent/experts", s.uploadExpertDef)
	a.DELETE("/agent/skills/:id", s.deleteSkillDef)
	a.DELETE("/agent/experts/:id", s.deleteExpertDef)
	a.GET("/agent/conversations", s.conversationList)
	a.GET("/agent/conversations/:id", s.conversationDetail)
	a.DELETE("/agent/conversations/:id", s.deleteConversation)
	a.POST("/agent/chat", s.chat)
	a.POST("/agent/stream", s.agentStream)
	a.GET("/agent/runs/:id", s.agentRunDetail)
	a.GET("/connectors", s.connectors)
	a.GET("/settings/models", s.modelSettings)
	a.PUT("/settings/models", s.saveModelSettings)
	a.POST("/settings/models/test", s.testModelSettings)
	a.POST("/settings/models/list", s.listAvailableModels)
	// 多来源模型配置：来源增删改、切换当前来源、单独测试某个来源。
	a.POST("/settings/models/providers", s.createModelProvider)
	a.PUT("/settings/models/providers/:id", s.updateModelProvider)
	a.DELETE("/settings/models/providers/:id", s.deleteModelProvider)
	a.POST("/settings/models/providers/:id/test", s.testModelProvider)
	a.PUT("/settings/models/selection", s.saveModelSelection)
	a.GET("/settings/web-search", s.webSearchSettings)
	a.PUT("/settings/web-search", s.saveWebSearchSettings)
	a.POST("/settings/web-search/test", s.testWebSearch)
	a.POST("/units/:id/web-import", s.webImport)
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			// started_at tells the caller whether this process is the build they
			// expect: a stale backend is the usual reason for a missing route.
			c.JSON(404, gin.H{"error": "API not found", "started_at": serverStartedAt})
			return
		}
		c.File(filepath.Join("frontend", "dist", "index.html"))
	})
	return r
}

// Run serves the router with basic connection timeouts. Write timeouts are
// left unset because chat completion requests can legitimately run long.
func (s *Server) Run(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.engine,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}

func (s *Server) ready(c *gin.Context) bool {
	if s.db == nil {
		c.JSON(503, gin.H{"error": "PG_DSN is not configured"})
		return false
	}
	return true
}

func (s *Server) units(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	// 知识库只是文档分组：列表直接带出文档数、文本块与最后索引时间。
	rows, e := s.db.Query(c, `SELECT u.unit_id,u.name,u.unit_type,u.industry,u.expert_id,u.skill_ids,u.tool_names,u.tags,
		(SELECT count(*) FROM document_units du WHERE du.unit_id=u.unit_id) AS document_count,
		(SELECT COALESCE(sum(d.chunk_count),0) FROM documents d
			JOIN document_units du2 ON du2.doc_id=d.doc_id WHERE du2.unit_id=u.unit_id) AS chunk_count,
		(SELECT max(d.indexed_at) FROM documents d
			JOIN document_units du3 ON du3.doc_id=d.doc_id WHERE du3.unit_id=u.unit_id) AS last_indexed_at,
		(SELECT COALESCE(json_agg(DISTINCT d.doc_type) FILTER (WHERE d.doc_type<>''), '[]'::json)
			FROM documents d JOIN document_units du4 ON du4.doc_id=d.doc_id WHERE du4.unit_id=u.unit_id) AS doc_types
		FROM units u ORDER BY u.name`)
	if e != nil {
		fail(c, e)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, n, t, i, expert string
		var skillRaw, toolRaw, tagRaw []byte
		var docCount, chunkCount int
		var lastIndexed *time.Time
		var docTypesRaw []byte
		if e = rows.Scan(&id, &n, &t, &i, &expert, &skillRaw, &toolRaw, &tagRaw,
			&docCount, &chunkCount, &lastIndexed, &docTypesRaw); e != nil {
			fail(c, e)
			return
		}
		docTypes := []string{}
		_ = json.Unmarshal(docTypesRaw, &docTypes)
		out = append(out, gin.H{
			"unit_id": id, "name": n, "unit_type": t, "industry": i, "expert_id": expert,
			"skill_ids": decodeSkillIDs(skillRaw), "tool_names": decodeSkillIDs(toolRaw), "tags": decodeSkillIDs(tagRaw),
			"document_count": docCount, "chunk_count": chunkCount, "last_indexed_at": lastIndexed,
			"doc_types": docTypes,
		})
	}
	if e = rows.Err(); e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, out)
}

func (s *Server) profile(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var name, typ, industry, expertID string
	var skillRaw, toolRaw, tagRaw []byte
	var audits, hazards int
	e := s.db.QueryRow(c, `SELECT u.name,u.unit_type,u.industry,u.expert_id,u.skill_ids,u.tool_names,u.tags,
		(SELECT count(*) FROM audit_records a WHERE a.unit_id=u.unit_id),
		(SELECT count(*) FROM hazards h WHERE h.unit_id=u.unit_id AND h.status NOT IN ('已整改','已关闭'))
		FROM units u WHERE u.unit_id=$1`, c.Param("id")).Scan(&name, &typ, &industry, &expertID, &skillRaw, &toolRaw, &tagRaw, &audits, &hazards)
	if e != nil {
		fail(c, e)
		return
	}
	chunks := 0
	if s.vectors != nil {
		docIDs, idErr := s.docIDsForUnits(c, []string{c.Param("id")})
		if idErr != nil {
			fail(c, idErr)
			return
		}
		chunks, e = s.vectors.Count(c, docIDs)
		if e != nil {
			fail(c, e)
			return
		}
	}
	skills := decodeSkillIDs(skillRaw)
	tools := decodeSkillIDs(toolRaw)
	docTypes, documents, lastIndexed := s.unitDocumentStats(c, c.Param("id"))
	c.JSON(200, gin.H{
		"unit_id": c.Param("id"), "name": name, "unit_type": typ, "industry": industry,
		"expert_id": expertID, "skill_ids": skills, "tool_names": tools, "tags": decodeSkillIDs(tagRaw),
		"tools": s.effectiveTools(expertID, skills, tools), "audit_count": audits, "open_hazards": hazards,
		"indexed_chunks": chunks, "document_count": documents, "doc_types": docTypes, "last_indexed_at": lastIndexed,
	})
}

func (s *Server) hazards(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	q := `SELECT id,hazard,status,severity,source,occurred_at FROM hazards WHERE unit_id=$1`
	args := []any{c.Param("id")}
	if st := c.Query("status"); st != "" {
		q += ` AND status=$2`
		args = append(args, st)
	}
	q += ` ORDER BY created_at DESC`
	rows, e := s.db.Query(c, q, args...)
	if e != nil {
		fail(c, e)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id int64
		var h, st, sv, src string
		var when *time.Time
		if e = rows.Scan(&id, &h, &st, &sv, &src, &when); e != nil {
			fail(c, e)
			return
		}
		out = append(out, gin.H{"id": id, "hazard": h, "status": st, "severity": sv, "source": src, "occurred_at": when})
	}
	if e = rows.Err(); e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, out)
}

func (s *Server) audits(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	rows, e := s.db.Query(c, `SELECT id,title,score,risk_level,payload,created_at FROM audit_records WHERE unit_id=$1 ORDER BY created_at DESC`, c.Param("id"))
	if e != nil {
		fail(c, e)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id int64
		var title, risk string
		var score *float64
		var payload []byte
		var at time.Time
		if e = rows.Scan(&id, &title, &score, &risk, &payload, &at); e != nil {
			fail(c, e)
			return
		}
		var p any
		_ = json.Unmarshal(payload, &p)
		out = append(out, gin.H{"id": id, "title": title, "score": score, "risk_level": risk, "payload": p, "created_at": at})
	}
	if e = rows.Err(); e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, out)
}

func fail(c *gin.Context, e error) {
	attrs := []any{
		"request_id", c.GetString(requestIDKey),
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"error", e,
	}
	if errors.Is(e, pgx.ErrNoRows) {
		logging.App().Warn("request_not_found", attrs...)
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	logging.App().Error("request_failed", append(attrs, "status", 500)...)
	if errors.Is(e, errEmbeddingConfig) || errors.Is(e, errChatConfig) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": e.Error()})
		return
	}
	// Keep database and dependency details in the log; do not leak them to the
	// client response.
	c.JSON(500, gin.H{"error": "internal server error"})
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// docLock serializes indexing for the same unit and filename so concurrent
// re-uploads cannot interleave their chunk writes.
func (s *Server) docLock(key string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	lock, ok := s.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	return lock
}
