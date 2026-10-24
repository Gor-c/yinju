package app

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/rag"
	"knowledge-base/internal/vectorstore"
)

// 这个文件是 app 层与 rag 层之间的唯一接缝：把 Server 持有的数据库、模型
// 客户端、环境配置翻译成 rag 的接口，不在这里写任何编排逻辑。
// 想加一路新召回（图谱、时间线、外部检索），在这里 append 一个 Channel 即可。

// retrievalTuning 把生效参数翻译成 rag.Tuning。参数来源是数据库（页面可改），
// 环境变量只在数据库缺失时兜底，因此这里不再直接读 env。
func (s *Server) retrievalTuning() rag.Tuning {
	t := rag.DefaultTuning()
	cfg := s.runtimeConfig()
	t.VectorK = cfg.VectorK
	t.KeywordK = cfg.KeywordK
	t.FusionK = cfg.FusionK
	t.MaxDist = cfg.MaxDistance
	t.MinOverlap = cfg.MinOverlap
	t.MinScore = cfg.MinScore
	return t
}

// Embed 让 Server 直接满足 rag.Embedder，不需要额外包装类型。
func (s *Server) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	return s.embedContext(ctx, texts)
}

// retrievalPipeline 组装一次检索。每次请求重建，因为通道需要捕获本次的
// 调优参数；Pipeline 本身是轻量结构，重建成本可忽略。
func (s *Server) retrievalPipeline(ctx context.Context) (*rag.Pipeline, error) {
	if s.vectors == nil {
		return nil, errors.New("vector store is not configured")
	}
	tuning := s.retrievalTuning()
	cfg := s.modelConfigFor(ctx)

	pipeline := &rag.Pipeline{
		Embedder: s,
		Tuning:   tuning,
		Identity: rag.Identity{Model: cfg.EmbedModel, Dim: vectorstore.EmbeddingDim()},
		OnDegrade: func(channel string, err error) {
			// 降级必须留痕：否则「为什么这次只返回了关键词结果」无处可查。
			logging.App().Warn("retrieval_degraded", "channel", channel, "error", err)
		},
		Channels: []rag.Channel{
			{
				Name: "vector",
				Fetch: func(ctx context.Context, req rag.Request) ([]rag.Match, error) {
					if len(req.Embedding) == 0 {
						return nil, errors.New("query embedding is unavailable")
					}
					return s.vectors.Search(ctx, req.DocIDs, req.Embedding, tuning.VectorK)
				},
			},
			{
				Name: "keyword",
				Fetch: func(ctx context.Context, req rag.Request) ([]rag.Match, error) {
					return s.keywordSearch(ctx, req.DocIDs, req.Query, tuning.KeywordK)
				},
			},
		},
	}
	if reranker := s.rerankAdapter(ctx); reranker != nil {
		pipeline.Reranker = reranker
	}
	return pipeline, nil
}

// hybridSearch 是检索的唯一入口：先校验向量空间身份，再交给 Pipeline。
// 无论成功失败都留一条 trace，让「为什么没答上来」事后可查。
func (s *Server) hybridSearch(ctx context.Context, docIDs []string, query string, k int) ([]vectorstore.Match, error) {
	matches, _, err := s.hybridSearchWithStats(ctx, docIDs, query, k)
	return matches, err
}

// hybridSearchWithStats 同 hybridSearch，但把分层计数一并带出去。
// 参数页的「试一次」要靠这四个数字解释结果：是没召回到，还是被阈值杀了。
func (s *Server) hybridSearchWithStats(ctx context.Context, docIDs []string, query string, k int) ([]vectorstore.Match, rag.Stats, error) {
	pipeline, err := s.retrievalPipeline(ctx)
	if err != nil {
		return nil, rag.Stats{}, err
	}
	if err := s.verifyIndexIdentity(ctx, docIDs, pipeline.Identity); err != nil {
		return nil, rag.Stats{}, err
	}

	started := time.Now()
	var stats rag.Stats
	pipeline.OnTrace = func(st rag.Stats) { stats = st }
	matches, searchErr := pipeline.Search(ctx, docIDs, query, k)
	s.recordRetrievalTrace(ctx, query, len(docIDs), stats, len(matches), searchErr != nil, time.Since(started))
	return matches, stats, searchErr
}

// httpReranker 把 /rerank HTTP 接口适配成 rag.Reranker。
// 输入截断属于协议约束（重排模型有窗口上限），因此留在适配器里，
// 不污染 Pipeline 的编排逻辑。
type httpReranker struct {
	server *Server
	cfg    rerankConfig
}

func (s *Server) rerankAdapter(ctx context.Context) rag.Reranker {
	cfg := s.rerankConfigFor(ctx)
	if cfg.BaseURL == "" {
		return nil
	}
	return httpReranker{server: s, cfg: cfg}
}

func (r httpReranker) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	trimmed := make([]string, len(docs))
	for i, doc := range docs {
		trimmed[i] = truncateRunes(doc, rerankMaxRunes)
	}
	scored, err := r.server.rerankRequest(ctx, r.cfg.BaseURL, r.cfg.APIKey, r.cfg.Model, query, trimmed)
	if err != nil {
		return nil, err
	}
	if len(scored) == 0 {
		return nil, errors.New("rerank API returned no results")
	}
	// 接口要求等长：服务端没返回分数的条目给 -1，让阈值过滤掉，
	// 而不是让它们以「未知分数」混进上下文。
	out := make([]float64, len(docs))
	for i := range out {
		out[i] = -1
	}
	for _, item := range scored {
		if item.Index < 0 || item.Index >= len(out) {
			continue
		}
		out[item.Index] = item.Score
	}
	return out, nil
}

// verifyIndexIdentity 确认要检索的文档是用当前嵌入模型建的索引。
//
// 为什么必须在检索前拦：换 embedding 模型后旧向量没有任何标记，两个向量
// 空间混算得到的是噪声，但系统不会报错——只表现为「检索突然变差了」。
// 这类故障靠人回忆是查不出来的，只能靠元数据比对。
//
// 老数据（未登记身份）放行但告警，避免升级当天全线不可用；一旦重新索引
// 就会写入身份，之后自动受保护。
func (s *Server) verifyIndexIdentity(ctx context.Context, docIDs []string, current rag.Identity) error {
	if s.db == nil || len(docIDs) == 0 {
		return nil
	}
	if err := current.Validate(); err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `SELECT COALESCE(embedding_model,''), COALESCE(embedding_dim,0), count(*)
		FROM documents WHERE doc_id = ANY($1) GROUP BY 1,2`, docIDs)
	if err != nil {
		// 元数据读不到不应该让检索整体不可用，但必须留痕。
		logging.App().Warn("retrieval_identity_check_failed", "error", err)
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var model string
		var dim int
		var count int
		if err := rows.Scan(&model, &dim, &count); err != nil {
			logging.App().Warn("retrieval_identity_scan_failed", "error", err)
			return nil
		}
		if model == "" || dim <= 0 {
			// 升级前建立的索引没有身份记录，放行一次。
			logging.App().Warn("retrieval_identity_unregistered",
				"documents", count, "hint", "重建索引后写入 embedding_model/embedding_dim")
			continue
		}
		indexed := rag.Identity{Model: model, Dim: dim}
		if !indexed.Equal(current) {
			return &rag.IdentityMismatch{Indexed: indexed, Current: current, Docs: count}
		}
	}
	if err := rows.Err(); err != nil {
		logging.App().Warn("retrieval_identity_rows_failed", "error", err)
	}
	return nil
}

// documentIdentityMatches 判断一份已索引文档的向量能否被当前模型查询。
// 未登记身份的旧数据按兼容处理（返回 true），避免升级当天全线不可用。
func (s *Server) documentIdentityMatches(ctx context.Context, docID string) (bool, error) {
	if s.db == nil {
		return true, nil
	}
	var model string
	var dim int
	err := s.db.QueryRow(ctx, `SELECT COALESCE(embedding_model,''), COALESCE(embedding_dim,0)
		FROM documents WHERE doc_id=$1`, docID).Scan(&model, &dim)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if model == "" || dim <= 0 {
		return true, nil
	}
	cfg := s.modelConfigFor(ctx)
	current := rag.Identity{Model: cfg.EmbedModel, Dim: vectorstore.EmbeddingDim()}
	return rag.Identity{Model: model, Dim: dim}.Equal(current), nil
}

// embeddingIdentityDescription 供设置页展示当前生效的向量空间身份，
// 让「换了模型但没重建索引」这件事在界面上可见。
func (s *Server) embeddingIdentityDescription(ctx context.Context) string {
	cfg := s.modelConfigFor(ctx)
	identity := rag.Identity{Model: cfg.EmbedModel, Dim: vectorstore.EmbeddingDim()}
	return identity.String()
}
