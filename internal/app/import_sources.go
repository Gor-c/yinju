package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

// 导入源把「一次性抓取」升级为「可重复同步」：记录远端条目的版本，
// 同步时只处理新增或有变化的文档，未变化的直接跳过，不重复下载与建任务。

const createImportSourcesTable = `CREATE TABLE IF NOT EXISTS import_sources (
	id bigserial PRIMARY KEY,
	unit_id text NOT NULL,
	kind text NOT NULL DEFAULT 'github',
	name text NOT NULL DEFAULT '',
	config jsonb NOT NULL DEFAULT '{}'::jsonb,
	secret text NOT NULL DEFAULT '',
	last_synced_at timestamptz,
	-- 定时同步：0 表示关闭；next_sync_at 为空表示等待第一次排期。
	sync_interval_minutes int NOT NULL DEFAULT 0,
	next_sync_at timestamptz,
	last_error text NOT NULL DEFAULT '',
	created_at timestamptz NOT NULL DEFAULT now(),
	updated_at timestamptz NOT NULL DEFAULT now()
)`

const createImportItemsTable = `CREATE TABLE IF NOT EXISTS import_items (
	source_id bigint NOT NULL REFERENCES import_sources(id) ON DELETE CASCADE,
	remote_id text NOT NULL,
	remote_rev text NOT NULL DEFAULT '',
	filename text NOT NULL DEFAULT '',
	-- 远端已删除但本地仍保留时打上时间戳，由用户确认后再清理。
	missing_at timestamptz,
	synced_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (source_id, remote_id)
)`

// ensureImportSourceSchema 建表（可重复执行），老库无需手工迁移。
func (s *Server) ensureImportSourceSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	for _, stmt := range []string{
		createImportSourcesTable,
		createImportItemsTable,
		`CREATE INDEX IF NOT EXISTS import_sources_unit_idx ON import_sources(unit_id)`,
		// 老库补列：定时排期与远端删除标记。
		`ALTER TABLE import_sources ADD COLUMN IF NOT EXISTS sync_interval_minutes int NOT NULL DEFAULT 0`,
		`ALTER TABLE import_sources ADD COLUMN IF NOT EXISTS next_sync_at timestamptz`,
		`ALTER TABLE import_sources ADD COLUMN IF NOT EXISTS last_error text NOT NULL DEFAULT ''`,
		`ALTER TABLE import_items ADD COLUMN IF NOT EXISTS missing_at timestamptz`,
		`CREATE INDEX IF NOT EXISTS import_sources_due_idx ON import_sources(next_sync_at)`,
	} {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("import_source_schema_failed", "error", err)
			return
		}
	}
}

// importSourceConfig 是来源的业务参数，按类型取其中一部分字段。
type importSourceConfig struct {
	Repo    string `json:"repo"`
	Owner   string `json:"owner"`
	Name    string `json:"name"`
	Ref     string `json:"ref"`
	Path    string `json:"path"`
	DocType string `json:"doc_type"`
	Limit   int    `json:"limit"`
}

type importSource struct {
	ID           int64
	UnitID       string
	Kind         string
	Name         string
	Config       importSourceConfig
	Secret       string
	LastSyncedAt *time.Time
	ItemCount    int
	// 定时同步相关。
	SyncInterval int
	NextSyncAt   *time.Time
	LastError    string
	// MissingCount 是远端已删除、本地仍保留的条目数。
	MissingCount int
}

func scanImportSource(row pgx.Row) (importSource, error) {
	var src importSource
	var raw []byte
	if err := row.Scan(&src.ID, &src.UnitID, &src.Kind, &src.Name, &raw, &src.Secret,
		&src.LastSyncedAt, &src.ItemCount, &src.SyncInterval, &src.NextSyncAt, &src.LastError, &src.MissingCount); err != nil {
		return importSource{}, err
	}
	_ = json.Unmarshal(raw, &src.Config)
	return src, nil
}

const importSourceColumns = `s.id, s.unit_id, s.kind, s.name, s.config, s.secret, s.last_synced_at,
	(SELECT count(*) FROM import_items i WHERE i.source_id = s.id),
	s.sync_interval_minutes, s.next_sync_at, s.last_error,
	(SELECT count(*) FROM import_items i WHERE i.source_id = s.id AND i.missing_at IS NOT NULL)`

func (s *Server) listImportSources(ctx context.Context, unitID string) ([]importSource, error) {
	if s.db == nil {
		return nil, nil
	}
	query := `SELECT ` + importSourceColumns + ` FROM import_sources s`
	args := []any{}
	if strings.TrimSpace(unitID) != "" {
		query += ` WHERE s.unit_id = $1`
		args = append(args, unitID)
	}
	query += ` ORDER BY s.id DESC`

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []importSource{}
	for rows.Next() {
		src, err := scanImportSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

func (s *Server) getImportSource(ctx context.Context, id int64) (importSource, error) {
	if s.db == nil {
		return importSource{}, errDatabaseNotConfigured
	}
	return scanImportSource(s.db.QueryRow(ctx,
		`SELECT `+importSourceColumns+` FROM import_sources s WHERE s.id = $1`, id))
}

func sourceJSON(src importSource) gin.H {
	// secret 只回是否已保存，不回原文。
	return gin.H{
		"id":                    src.ID,
		"unit_id":               src.UnitID,
		"kind":                  src.Kind,
		"name":                  src.Name,
		"config":                src.Config,
		"has_secret":            src.Secret != "",
		"last_synced_at":        src.LastSyncedAt,
		"item_count":            src.ItemCount,
		"sync_interval_minutes": src.SyncInterval,
		"next_sync_at":          src.NextSyncAt,
		"last_error":            src.LastError,
		"missing_count":         src.MissingCount,
	}
}

func (s *Server) importSourceList(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	items, err := s.listImportSources(c, c.Query("unit_id"))
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]gin.H, 0, len(items))
	for _, src := range items {
		out = append(out, sourceJSON(src))
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

type importSourceInput struct {
	UnitID    string             `json:"unit_id"`
	Kind      string             `json:"kind"`
	Name      string             `json:"name"`
	Config    importSourceConfig `json:"config"`
	Token     string             `json:"token"`
	SaveToken bool               `json:"save_token"`
}

func (s *Server) createImportSource(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in importSourceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	if in.Kind != "yuque" && in.Kind != "github" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind 必须是 yuque 或 github"})
		return
	}
	unit, err := s.importUnit(c, in.UnitID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateSourceConfig(in.Kind, in.Config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	raw, _ := json.Marshal(in.Config)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = defaultSourceName(in.Kind, in.Config)
	}
	secret := ""
	if in.SaveToken {
		secret = strings.TrimSpace(in.Token)
	}

	var id int64
	err = s.db.QueryRow(c, `INSERT INTO import_sources(unit_id,kind,name,config,secret)
		VALUES($1,$2,$3,$4,$5) RETURNING id`, unit, in.Kind, name, raw, secret).Scan(&id)
	if err != nil {
		fail(c, err)
		return
	}
	src, err := s.getImportSource(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("import_source_created", "id", id, "kind", in.Kind, "unit_id", unit, "name", name)
	c.JSON(http.StatusOK, gin.H{"source": sourceJSON(src)})
}

func (s *Server) deleteImportSource(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	if _, err := s.db.Exec(c, `DELETE FROM import_sources WHERE id=$1`, id); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "id": id})
}

// syncOutcome 是一次同步的结果汇总。
type syncOutcome struct {
	Queued    int
	Skipped   int
	Unchanged int
	// Missing 是本次新发现「远端已删除」的条目数。
	Missing int
	Failed  []importFailure
}

// syncImportSource 执行增量同步：只处理新增或有变化的条目。
func (s *Server) syncImportSource(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	var in struct {
		Token  string `json:"token"`
		DryRun bool   `json:"dry_run"`
	}
	_ = c.ShouldBindJSON(&in)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 180*time.Second)
	defer cancel()
	out, err := s.syncSourceByID(ctx, id, strings.TrimSpace(in.Token), in.DryRun)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "导入源不存在"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if in.DryRun {
		c.JSON(http.StatusOK, gin.H{"dry_run": true, "pending": out.Queued, "unchanged": out.Unchanged})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"queued":    out.Queued,
		"skipped":   out.Skipped,
		"unchanged": out.Unchanged,
		"missing":   out.Missing,
		"failed":    out.Failed,
	})
}

// syncSourceByID 供接口与定时任务共用。token 为空时用已保存的密钥。
func (s *Server) syncSourceByID(ctx context.Context, id int64, token string, dryRun bool) (syncOutcome, error) {
	src, err := s.getImportSource(ctx, id)
	if err != nil {
		return syncOutcome{}, err
	}
	if strings.TrimSpace(token) == "" {
		token = src.Secret
	}
	known := s.loadImportRevisions(ctx, src.ID)
	items, unchanged, remoteIDs, err := s.collectImportItems(ctx, src, token, known)
	if err != nil {
		s.recordSyncError(src.ID, err.Error())
		return syncOutcome{}, err
	}
	if dryRun {
		return syncOutcome{Unchanged: unchanged, Queued: len(items)}, nil
	}

	result := s.runImport(ctx, src.UnitID, importDocType(src.Config.DocType), items)
	if len(items) > 0 {
		s.recordImportRevisions(ctx, src.ID, items)
	}
	missing := s.markMissingItems(ctx, src.ID, remoteIDs)
	// 排下一次：用数据库时间，避免多实例时钟漂移。
	if _, err := s.db.Exec(ctx, `UPDATE import_sources
		SET last_synced_at=now(), updated_at=now(), last_error='',
			next_sync_at=CASE WHEN sync_interval_minutes > 0
				THEN now() + make_interval(mins => sync_interval_minutes) ELSE NULL END
		WHERE id=$1`, src.ID); err != nil {
		logging.App().Warn("import_source_schedule_update_failed", "source_id", src.ID, "error", err)
	}
	logging.App().Info("import_source_synced",
		"id", src.ID, "kind", src.Kind, "pending", len(items), "unchanged", unchanged,
		"queued", result.Queued, "failed", len(result.Failed), "missing", missing)
	return syncOutcome{
		Queued: result.Queued, Skipped: result.Skipped,
		Unchanged: unchanged, Missing: missing, Failed: result.Failed,
	}, nil
}

func (s *Server) recordSyncError(sourceID int64, message string) {
	if s.db == nil {
		return
	}
	_, err := s.db.Exec(context.Background(), `UPDATE import_sources
		SET last_error=$2, updated_at=now(),
			next_sync_at=CASE WHEN sync_interval_minutes > 0
				THEN now() + make_interval(mins => GREATEST(sync_interval_minutes, 5)) ELSE NULL END
		WHERE id=$1`, sourceID, truncateRunes(message, 300))
	if err != nil {
		logging.App().Warn("import_source_error_record_failed", "source_id", sourceID, "error", err)
	}
}

// markMissingItems 把「本地有、远端没有」的条目打上 missing_at。
// 远端列表为空时不动：那更像是抓取异常，而不是真的全删了。
func (s *Server) markMissingItems(ctx context.Context, sourceID int64, remoteIDs []string) int {
	if s.db == nil || len(remoteIDs) == 0 {
		return 0
	}
	if _, err := s.db.Exec(ctx, `UPDATE import_items SET missing_at=NULL
		WHERE source_id=$1 AND missing_at IS NOT NULL AND remote_id = ANY($2)`, sourceID, remoteIDs); err != nil {
		logging.App().Warn("import_items_restore_failed", "source_id", sourceID, "error", err)
	}
	tag, err := s.db.Exec(ctx, `UPDATE import_items SET missing_at=now()
		WHERE source_id=$1 AND missing_at IS NULL AND NOT (remote_id = ANY($2))`, sourceID, remoteIDs)
	if err != nil {
		logging.App().Warn("import_items_mark_missing_failed", "source_id", sourceID, "error", err)
		return 0
	}
	return int(tag.RowsAffected())
}

// cleanupImportSource 清理「远端已删除」的条目：删除对应文档并移除同步记录。
func (s *Server) cleanupImportSource(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	src, err := s.getImportSource(c, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "导入源不存在"})
			return
		}
		fail(c, err)
		return
	}

	rows, err := s.db.Query(c, `SELECT remote_id, filename FROM import_items
		WHERE source_id=$1 AND missing_at IS NOT NULL ORDER BY remote_id`, id)
	if err != nil {
		fail(c, err)
		return
	}
	type target struct{ remoteID, filename string }
	targets := []target{}
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.remoteID, &t.filename); err == nil {
			targets = append(targets, t)
		}
	}
	rows.Close()

	removed, failed := 0, []importFailure{}
	for _, t := range targets {
		if err := s.removeDocumentByUnitFile(c, src.UnitID, t.filename); err != nil {
			failed = append(failed, importFailure{Name: t.filename, Error: err.Error()})
			continue
		}
		if _, err := s.db.Exec(c, `DELETE FROM import_items WHERE source_id=$1 AND remote_id=$2`, id, t.remoteID); err != nil {
			failed = append(failed, importFailure{Name: t.filename, Error: err.Error()})
			continue
		}
		removed++
	}
	logging.App().Info("import_source_cleaned", "id", id, "removed", removed, "failed", len(failed))
	c.JSON(http.StatusOK, gin.H{"removed": removed, "failed": failed})
}

// removeDocumentByUnitFile 删除「某知识库下的某文件」，与文档库删除语义一致。
func (s *Server) removeDocumentByUnitFile(ctx context.Context, unit, filename string) error {
	if s.vectors == nil {
		return errString("向量库未配置，无法删除文档")
	}
	ref, err := s.documentRefByUnitFile(ctx, unit, filename)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 已经不存在，视为清理成功
		}
		return err
	}
	if err := s.unlinkDocumentUnit(ctx, ref.DocID, unit); err != nil {
		return err
	}
	_, err = s.cleanupOrphanDocument(ctx, ref.DocID)
	return err
}

// updateImportSchedule 设置定时同步间隔（分钟，0 为关闭）。
func (s *Server) updateImportSchedule(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id 无效"})
		return
	}
	var in struct {
		IntervalMinutes int `json:"interval_minutes"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	if in.IntervalMinutes < 0 || in.IntervalMinutes > 10080 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "间隔需在 0–10080 分钟之间（0 表示关闭）"})
		return
	}
	src, err := s.getImportSource(c, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "导入源不存在"})
			return
		}
		fail(c, err)
		return
	}
	// 开启定时同步但没保存 Token：语雀一定跑不起来，提前给出提示。
	if in.IntervalMinutes > 0 && src.Secret == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "该来源没有保存 Token，无法定时同步；请重新保存来源并勾选「保存 Token」"})
		return
	}
	if _, err := s.db.Exec(c, `UPDATE import_sources
		SET sync_interval_minutes=$2, updated_at=now(),
			next_sync_at=CASE WHEN $2 > 0 THEN now() + make_interval(mins => $2) ELSE NULL END
		WHERE id=$1`, id, in.IntervalMinutes); err != nil {
		fail(c, err)
		return
	}
	updated, err := s.getImportSource(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("import_source_schedule_updated", "id", id, "interval_minutes", in.IntervalMinutes)
	c.JSON(http.StatusOK, gin.H{"source": sourceJSON(updated)})
}

// loadImportRevisions 一次取出该来源已同步的条目版本，避免逐条查询。
func (s *Server) loadImportRevisions(ctx context.Context, sourceID int64) map[string]string {
	out := map[string]string{}
	if s.db == nil || sourceID <= 0 {
		return out
	}
	rows, err := s.db.Query(ctx, `SELECT remote_id, remote_rev FROM import_items WHERE source_id=$1`, sourceID)
	if err != nil {
		logging.App().Warn("import_items_load_failed", "source_id", sourceID, "error", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, rev string
		if err := rows.Scan(&id, &rev); err == nil {
			out[id] = rev
		}
	}
	return out
}

func (s *Server) recordImportRevisions(ctx context.Context, sourceID int64, items []importItem) {
	if s.db == nil || sourceID <= 0 || len(items) == 0 {
		return
	}
	batch := &pgx.Batch{}
	for _, item := range items {
		batch.Queue(`INSERT INTO import_items(source_id,remote_id,remote_rev,filename,synced_at)
			VALUES($1,$2,$3,$4,now())
			ON CONFLICT(source_id,remote_id) DO UPDATE
			SET remote_rev=EXCLUDED.remote_rev, filename=EXCLUDED.filename, synced_at=now()`,
			sourceID, item.RemoteID, item.Revision, sanitizeImportName(item.Name))
	}
	results := s.db.SendBatch(ctx, batch)
	defer results.Close()
	for range items {
		if _, err := results.Exec(); err != nil {
			logging.App().Warn("import_item_record_failed", "source_id", sourceID, "error", err)
			return
		}
	}
}

// collectImportItems 按来源类型抓取待导入条目。
// unchanged 是版本未变化的条目数；remoteIDs 是远端当前全部条目（用于判断「远端已删除」）。
func (s *Server) collectImportItems(ctx context.Context, src importSource, token string, known map[string]string) ([]importItem, int, []string, error) {
	limit := importLimit(src.Config.Limit)
	if known == nil {
		known = map[string]string{}
	}
	unchanged := 0

	switch src.Kind {
	case "yuque":
		if strings.TrimSpace(token) == "" {
			return nil, 0, nil, errString("请填写语雀 Token")
		}
		docs, err := fetchYuqueDocList(ctx, token, src.Config.Repo, limit)
		if err != nil {
			return nil, 0, nil, errString("读取语雀文档列表失败：" + err.Error())
		}
		// 目录树用于把文档放进分组路径；拿不到就退回扁平标题，不影响导入。
		folders, tocErr := fetchYuqueTOC(ctx, token, src.Config.Repo)
		if tocErr != nil {
			logging.App().Warn("yuque_toc_failed", "repo", src.Config.Repo, "error", tocErr)
		}

		items := make([]importItem, 0, len(docs))
		remoteIDs := make([]string, 0, len(docs))
		for _, doc := range docs {
			remoteID := strconv.Itoa(doc.ID)
			remoteIDs = append(remoteIDs, remoteID)
			rev := doc.UpdatedAt
			if rev == "" {
				rev = doc.Slug
			}
			// 版本为空说明远端没提供可比对的信息，宁可重复导入也不要漏。
			if rev != "" && known[remoteID] == rev {
				unchanged++
				continue
			}
			content, format, err := fetchYuqueDoc(ctx, token, src.Config.Repo, doc.ID)
			if err != nil {
				logging.App().Warn("yuque_doc_fetch_failed", "doc_id", doc.ID, "error", err)
				continue
			}
			name := doc.Title
			if folder := folders[doc.ID]; folder != "" {
				name = folder + "/" + doc.Title
			}
			if format == "lake" || format == "html" {
				name += ".html"
			} else {
				name += ".md"
			}
			items = append(items, importItem{
				Name: name, Content: content, RemoteID: remoteID, Revision: rev,
				Source: "yuque:" + doc.Slug,
			})
		}
		return items, unchanged, remoteIDs, nil

	case "github":
		ref := strings.TrimSpace(src.Config.Ref)
		if ref == "" {
			ref = "HEAD"
		}
		files, err := listGithubFiles(ctx, token, src.Config.Owner, src.Config.Name, ref, strings.Trim(src.Config.Path, "/"), limit)
		if err != nil {
			return nil, 0, nil, errString("读取仓库文件列表失败：" + err.Error())
		}
		items := make([]importItem, 0, len(files))
		remoteIDs := make([]string, 0, len(files))
		for _, file := range files {
			remoteIDs = append(remoteIDs, file.Path)
			if file.SHA != "" && known[file.Path] == file.SHA {
				unchanged++
				continue
			}
			content, err := downloadGithubFile(ctx, token, src.Config.Owner, src.Config.Name, ref, file.Path)
			if err != nil {
				logging.App().Warn("github_file_fetch_failed", "path", file.Path, "error", err)
				continue
			}
			items = append(items, importItem{
				Name: file.Path, Content: content, RemoteID: file.Path, Revision: file.SHA,
				Source: "github:" + file.Path,
			})
		}
		return items, unchanged, remoteIDs, nil
	}
	return nil, 0, nil, errString("不支持的来源类型：" + src.Kind)
}

func validateSourceConfig(kind string, cfg importSourceConfig) error {
	switch kind {
	case "yuque":
		parts := strings.Split(strings.Trim(strings.TrimSpace(cfg.Repo), "/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return errString("语雀知识库格式应为 namespace/仓库名")
		}
	case "github":
		if strings.TrimSpace(cfg.Owner) == "" || strings.TrimSpace(cfg.Name) == "" {
			return errString("请填写 GitHub owner 与仓库名")
		}
	}
	return nil
}

func defaultSourceName(kind string, cfg importSourceConfig) string {
	if kind == "yuque" {
		return "语雀 · " + strings.Trim(cfg.Repo, "/")
	}
	return "GitHub · " + strings.TrimSpace(cfg.Owner) + "/" + strings.TrimSpace(cfg.Name)
}

// errString 让校验错误保持简单文本。
type errString string

func (e errString) Error() string { return string(e) }

// 定时同步的轮询间隔：一分钟一次，足够覆盖分钟级调度，也不会给数据库带来压力。
const importSchedulerInterval = time.Minute

// runImportScheduler 常驻扫描到期的导入源。多实例部署时靠 next_sync_at 的
// UPDATE 抢占（先更新者生效），因此同一来源不会被重复执行两次。
func (s *Server) runImportScheduler(ctx context.Context) {
	ticker := time.NewTicker(importSchedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDueImportSources(ctx)
		}
	}
}

func (s *Server) runDueImportSources(ctx context.Context) {
	if s.db == nil {
		return
	}
	dueCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := s.db.Query(dueCtx, `SELECT id FROM import_sources
		WHERE sync_interval_minutes > 0 AND (next_sync_at IS NULL OR next_sync_at <= now())
		ORDER BY next_sync_at NULLS FIRST LIMIT 3`)
	if err != nil {
		logging.App().Warn("import_scheduler_scan_failed", "error", err)
		return
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		// 先占位再执行：把 next_sync_at 推到下个周期，避免下一轮 tick 重复领取。
		if _, err := s.db.Exec(dueCtx, `UPDATE import_sources
			SET next_sync_at=now() + make_interval(mins => GREATEST(sync_interval_minutes, 5))
			WHERE id=$1`, id); err != nil {
			continue
		}
		syncCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		out, err := s.syncSourceByID(syncCtx, id, "", false)
		cancel()
		if err != nil {
			logging.App().Warn("import_scheduler_sync_failed", "id", id, "error", err)
			continue
		}
		logging.App().Info("import_scheduler_synced", "id", id,
			"queued", out.Queued, "unchanged", out.Unchanged, "missing", out.Missing)
	}
}
