package app

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

// runtimeConfig 是一次运行真正生效的参数。
//
// 优先级：数据库（页面可改）> 环境变量 > 代码默认。之所以把参数从只读的
// 环境变量搬到数据库，是因为「调了参数但不知道改没改对」和「改完看不见
// 效果」是调参最常见的两个死胡同——所以既要能改，也要留下改动痕迹。
type runtimeConfig struct {
	VectorK        int
	KeywordK       int
	FusionK        int
	MaxDistance    float64
	MinOverlap     float64
	MinScore       float64
	ChunkSize      int
	ChunkOverlap   int
	MemoryMessages int
	MemoryRunes    int
	MemorySummary  bool
	OutlineEnabled bool
}

func defaultRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		VectorK:        30,
		KeywordK:       30,
		FusionK:        20,
		MaxDistance:    0.55,
		MinOverlap:     0.28,
		MinScore:       0.1,
		ChunkSize:      1800,
		ChunkOverlap:   240,
		MemoryMessages: 20,
		MemoryRunes:    4000,
		MemorySummary:  true,
		OutlineEnabled: true,
	}
}

// runtimeConfig 返回当前生效参数。带 5 秒缓存：参数不是热路径上的高频读取，
// 但每次检索都查库没必要；多实例场景下最多滞后一个 TTL。
func (s *Server) runtimeConfig() runtimeConfig {
	s.runtimeMu.RLock()
	if !s.runtimeAt.IsZero() && time.Since(s.runtimeAt) < 5*time.Second {
		cfg := s.runtimeCfg
		s.runtimeMu.RUnlock()
		return cfg
	}
	s.runtimeMu.RUnlock()

	cfg := defaultRuntimeConfig()
	applyRuntimeEnv(&cfg)
	if s.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.loadRuntimeConfigRow(ctx, &cfg); err != nil {
			logging.App().Warn("runtime_config_load_failed", "error", err)
		}
	}

	s.runtimeMu.Lock()
	s.runtimeCfg = cfg
	s.runtimeAt = time.Now()
	s.runtimeMu.Unlock()
	return cfg
}

// applyRuntimeEnv 让环境变量作为数据库缺失时的兜底，保持老部署行为不变。
func applyRuntimeEnv(cfg *runtimeConfig) {
	cfg.VectorK = envIntValue("RETRIEVAL_VECTOR_K", cfg.VectorK)
	cfg.KeywordK = envIntValue("RETRIEVAL_KEYWORD_K", cfg.KeywordK)
	cfg.FusionK = envIntValue("RETRIEVAL_FUSION_K", cfg.FusionK)
	cfg.MaxDistance = envFloat("RETRIEVAL_MAX_DISTANCE", cfg.MaxDistance)
	cfg.MinOverlap = envFloat("RETRIEVAL_MIN_OVERLAP", cfg.MinOverlap)
	cfg.MinScore = envFloat("RERANK_MIN_SCORE", cfg.MinScore)
	cfg.ChunkSize = envIntValue("CHUNK_SIZE", cfg.ChunkSize)
	cfg.ChunkOverlap = envIntValue("CHUNK_OVERLAP", cfg.ChunkOverlap)
}

func (s *Server) loadRuntimeConfigRow(ctx context.Context, cfg *runtimeConfig) error {
	var found runtimeConfig
	err := s.db.QueryRow(ctx, `SELECT vector_k,keyword_k,fusion_k,max_distance,min_overlap,min_score,
			chunk_size,chunk_overlap,memory_messages,memory_runes,memory_summary,outline_enabled
		FROM retrieval_settings WHERE id=1`).Scan(
		&found.VectorK, &found.KeywordK, &found.FusionK,
		&found.MaxDistance, &found.MinOverlap, &found.MinScore,
		&found.ChunkSize, &found.ChunkOverlap,
		&found.MemoryMessages, &found.MemoryRunes, &found.MemorySummary, &found.OutlineEnabled)
	if err != nil {
		return err
	}
	*cfg = found
	return nil
}

func (s *Server) ensureRuntimeSettingsSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	// 参数表与三张留痕表一起建：留痕表缺失时必须立刻暴露，
	// 否则「改了参数但没有记录」会等到排查问题时才发现。
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS retrieval_settings (
			id smallint PRIMARY KEY DEFAULT 1,
			vector_k integer NOT NULL DEFAULT 30,
			keyword_k integer NOT NULL DEFAULT 30,
			fusion_k integer NOT NULL DEFAULT 20,
			max_distance double precision NOT NULL DEFAULT 0.55,
			min_overlap double precision NOT NULL DEFAULT 0.28,
			min_score double precision NOT NULL DEFAULT 0.1,
			chunk_size integer NOT NULL DEFAULT 1800,
			chunk_overlap integer NOT NULL DEFAULT 240,
			memory_messages integer NOT NULL DEFAULT 20,
			memory_runes integer NOT NULL DEFAULT 4000,
			memory_summary boolean NOT NULL DEFAULT true,
			outline_enabled boolean NOT NULL DEFAULT true,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`ALTER TABLE retrieval_settings ADD COLUMN IF NOT EXISTS memory_summary boolean NOT NULL DEFAULT true`,
		`CREATE TABLE IF NOT EXISTS setting_changes (
			id bigserial PRIMARY KEY,
			key text NOT NULL,
			old_value text NOT NULL DEFAULT '',
			new_value text NOT NULL DEFAULT '',
			source text NOT NULL DEFAULT 'ui',
			changed_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS setting_changes_changed_idx ON setting_changes(changed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS retrieval_traces (
			id bigserial PRIMARY KEY,
			request_id text NOT NULL DEFAULT '',
			query text NOT NULL DEFAULT '',
			doc_count integer NOT NULL DEFAULT 0,
			unit_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			vector_hits integer NOT NULL DEFAULT 0,
			keyword_hits integer NOT NULL DEFAULT 0,
			fused integer NOT NULL DEFAULT 0,
			reranked integer NOT NULL DEFAULT 0,
			kept integer NOT NULL DEFAULT 0,
			no_evidence boolean NOT NULL DEFAULT false,
			degraded text NOT NULL DEFAULT '',
			duration_ms integer NOT NULL DEFAULT 0,
			created_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS retrieval_traces_created_idx ON retrieval_traces(created_at DESC)`,
		// 老库补列：表已存在时 CREATE TABLE IF NOT EXISTS 不会加列。
		`ALTER TABLE retrieval_traces ADD COLUMN IF NOT EXISTS doc_count integer NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS doc_outlines (
			doc_id text PRIMARY KEY,
			filename text NOT NULL DEFAULT '',
			entries jsonb NOT NULL DEFAULT '[]'::jsonb,
			rule_version text NOT NULL DEFAULT '',
			extracted_at timestamptz NOT NULL DEFAULT now()
		)`,
		`INSERT INTO retrieval_settings(id) VALUES(1) ON CONFLICT (id) DO NOTHING`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("runtime_settings_schema_failed", "error", err)
			return
		}
	}
}

// runtimeValues 是参数的统一输出形状，GET 与 PUT 共用，
// 避免保存接口返回 Go 字段名（MemoryMessages）而页面读不到。
func runtimeValues(cfg runtimeConfig) gin.H {
	return gin.H{
		"vector_k": cfg.VectorK, "keyword_k": cfg.KeywordK, "fusion_k": cfg.FusionK,
		"max_distance": cfg.MaxDistance, "min_overlap": cfg.MinOverlap, "min_score": cfg.MinScore,
		"chunk_size": cfg.ChunkSize, "chunk_overlap": cfg.ChunkOverlap,
		"memory_messages": cfg.MemoryMessages, "memory_runes": cfg.MemoryRunes,
		"memory_summary":  cfg.MemorySummary,
		"outline_enabled": cfg.OutlineEnabled,
	}
}

// saveRuntimeConfig 落库并逐字段记录变更，返回实际改变的字段名。
// 没变的字段不写留痕——否则每次点保存都会产生一堆噪声。
func (s *Server) saveRuntimeConfig(ctx context.Context, in runtimeConfig) ([]string, error) {
	var old runtimeConfig
	if err := s.loadRuntimeConfigRow(ctx, &old); err != nil {
		return nil, err
	}
	_, err := s.db.Exec(ctx, `UPDATE retrieval_settings SET
			vector_k=$1,keyword_k=$2,fusion_k=$3,max_distance=$4,min_overlap=$5,min_score=$6,
			chunk_size=$7,chunk_overlap=$8,memory_messages=$9,memory_runes=$10,memory_summary=$11,outline_enabled=$12,
			updated_at=now() WHERE id=1`,
		in.VectorK, in.KeywordK, in.FusionK, in.MaxDistance, in.MinOverlap, in.MinScore,
		in.ChunkSize, in.ChunkOverlap, in.MemoryMessages, in.MemoryRunes, in.MemorySummary, in.OutlineEnabled)
	if err != nil {
		return nil, err
	}

	changes := diffRuntimeConfig(old, in)
	for _, c := range changes {
		if _, err := s.db.Exec(ctx, `INSERT INTO setting_changes(key,old_value,new_value,source)
			VALUES($1,$2,$3,'ui')`, c[0], c[1], c[2]); err != nil {
			logging.App().Warn("setting_change_log_failed", "key", c[0], "error", err)
		}
	}

	s.runtimeMu.Lock()
	s.runtimeAt = time.Time{}
	s.runtimeMu.Unlock()
	return firstElements(changes), nil
}

func diffRuntimeConfig(old, next runtimeConfig) [][3]string {
	pairs := [][3]string{
		{"vector_k", strconv.Itoa(old.VectorK), strconv.Itoa(next.VectorK)},
		{"keyword_k", strconv.Itoa(old.KeywordK), strconv.Itoa(next.KeywordK)},
		{"fusion_k", strconv.Itoa(old.FusionK), strconv.Itoa(next.FusionK)},
		{"max_distance", fmtFloat(old.MaxDistance), fmtFloat(next.MaxDistance)},
		{"min_overlap", fmtFloat(old.MinOverlap), fmtFloat(next.MinOverlap)},
		{"min_score", fmtFloat(old.MinScore), fmtFloat(next.MinScore)},
		{"chunk_size", strconv.Itoa(old.ChunkSize), strconv.Itoa(next.ChunkSize)},
		{"chunk_overlap", strconv.Itoa(old.ChunkOverlap), strconv.Itoa(next.ChunkOverlap)},
		{"memory_messages", strconv.Itoa(old.MemoryMessages), strconv.Itoa(next.MemoryMessages)},
		{"memory_runes", strconv.Itoa(old.MemoryRunes), strconv.Itoa(next.MemoryRunes)},
		{"memory_summary", strconv.FormatBool(old.MemorySummary), strconv.FormatBool(next.MemorySummary)},
		{"outline_enabled", strconv.FormatBool(old.OutlineEnabled), strconv.FormatBool(next.OutlineEnabled)},
	}
	out := make([][3]string, 0, len(pairs))
	for _, p := range pairs {
		if p[1] != p[2] {
			out = append(out, p)
		}
	}
	return out
}

func firstElements(rows [][3]string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r[0])
	}
	return out
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// retrievalSettingsView 是给页面的结构：值 + 每个字段的可调范围。
// 范围写在这里而不是前端，避免前后端各维护一份校验。
func (s *Server) retrievalSettingsView(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	cfg := s.runtimeConfig()
	c.JSON(http.StatusOK, gin.H{
		"values": runtimeValues(cfg),
		"limits": gin.H{
			"vector_k":        gin.H{"min": 5, "max": 100, "step": 5},
			"keyword_k":       gin.H{"min": 5, "max": 100, "step": 5},
			"fusion_k":        gin.H{"min": 5, "max": 100, "step": 5},
			"max_distance":    gin.H{"min": 0, "max": 1, "step": 0.05},
			"min_overlap":     gin.H{"min": 0, "max": 1, "step": 0.02},
			"min_score":       gin.H{"min": 0, "max": 1, "step": 0.05},
			"chunk_size":      gin.H{"min": 300, "max": 8000, "step": 100},
			"chunk_overlap":   gin.H{"min": 0, "max": 1000, "step": 20},
			"memory_messages": gin.H{"min": 0, "max": 100, "step": 2},
			"memory_runes":    gin.H{"min": 500, "max": 20000, "step": 500},
		},
		"updated_at": s.retrievalSettingsUpdatedAt(c),
		"changes":    s.recentSettingChanges(c, 10),
	})
}

func (s *Server) saveRetrievalSettings(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in struct {
		VectorK        *int     `json:"vector_k"`
		KeywordK       *int     `json:"keyword_k"`
		FusionK        *int     `json:"fusion_k"`
		MaxDistance    *float64 `json:"max_distance"`
		MinOverlap     *float64 `json:"min_overlap"`
		MinScore       *float64 `json:"min_score"`
		ChunkSize      *int     `json:"chunk_size"`
		ChunkOverlap   *int     `json:"chunk_overlap"`
		MemoryMessages *int     `json:"memory_messages"`
		MemoryRunes    *int     `json:"memory_runes"`
		MemorySummary  *bool    `json:"memory_summary"`
		OutlineEnabled *bool    `json:"outline_enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	next := s.runtimeConfig()
	if in.VectorK != nil {
		next.VectorK = clampInt(*in.VectorK, 5, 100)
	}
	if in.KeywordK != nil {
		next.KeywordK = clampInt(*in.KeywordK, 5, 100)
	}
	if in.FusionK != nil {
		next.FusionK = clampInt(*in.FusionK, 5, 100)
	}
	if in.MaxDistance != nil {
		next.MaxDistance = clampFloat(*in.MaxDistance, 0, 1)
	}
	if in.MinOverlap != nil {
		next.MinOverlap = clampFloat(*in.MinOverlap, 0, 1)
	}
	if in.MinScore != nil {
		next.MinScore = clampFloat(*in.MinScore, 0, 1)
	}
	if in.ChunkSize != nil {
		next.ChunkSize = clampInt(*in.ChunkSize, 300, 8000)
	}
	if in.ChunkOverlap != nil {
		next.ChunkOverlap = clampInt(*in.ChunkOverlap, 0, 1000)
	}
	if in.MemoryMessages != nil {
		next.MemoryMessages = clampInt(*in.MemoryMessages, 0, 100)
	}
	if in.MemoryRunes != nil {
		next.MemoryRunes = clampInt(*in.MemoryRunes, 500, 20000)
	}
	if in.MemorySummary != nil {
		next.MemorySummary = *in.MemorySummary
	}
	if in.OutlineEnabled != nil {
		next.OutlineEnabled = *in.OutlineEnabled
	}

	changed, err := s.saveRuntimeConfig(c, next)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"changed": changed, "values": runtimeValues(next)})
}

func (s *Server) retrievalSettingsUpdatedAt(ctx context.Context) any {
	var at *time.Time
	_ = s.db.QueryRow(ctx, `SELECT updated_at FROM retrieval_settings WHERE id=1`).Scan(&at)
	return at
}

func (s *Server) recentSettingChanges(ctx context.Context, limit int) []gin.H {
	rows, err := s.db.Query(ctx, `SELECT key,old_value,new_value,source,changed_at
		FROM setting_changes ORDER BY changed_at DESC LIMIT $1`, limit)
	if err != nil {
		return []gin.H{}
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var key, oldV, newV, source string
		var at time.Time
		if err := rows.Scan(&key, &oldV, &newV, &source, &at); err != nil {
			return out
		}
		out = append(out, gin.H{"key": key, "old": oldV, "new": newV, "source": source, "changed_at": at})
	}
	return out
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// 以下三个是链路里真实的取值入口：调用方不再直接读环境变量。

func (s *Server) chunkSize() int      { return s.runtimeConfig().ChunkSize }
func (s *Server) chunkOverlap() int   { return s.runtimeConfig().ChunkOverlap }
func (s *Server) memoryMessages() int { return s.runtimeConfig().MemoryMessages }
func (s *Server) memoryRunes() int    { return s.runtimeConfig().MemoryRunes }
