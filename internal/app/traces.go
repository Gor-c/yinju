package app

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/rag"
)

// 检索留痕：每一次检索都落一条，记下「召回多少、融合多少、重排后多少、
// 最后留下多少」。这四层数字是唯一能回答「为什么这个库答不上来」的东西——
// 只看最终结果，没人分得清是没召回到还是被阈值杀了。

func (s *Server) recordRetrievalTrace(ctx context.Context, query string, docCount int, stats rag.Stats, kept int, failed bool, duration time.Duration) {
	if s.db == nil {
		return
	}
	// 用独立超时而不是请求上下文：请求被取消时，留痕反而更需要写成功。
	writeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	degraded := strings.Join(stats.Degraded, ",")
	if failed && degraded == "" {
		degraded = "all-channels-failed"
	}
	if _, err := s.db.Exec(writeCtx, `INSERT INTO retrieval_traces(
			query,doc_count,vector_hits,keyword_hits,fused,reranked,kept,no_evidence,degraded,duration_ms)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		truncateRunes(query, 500), docCount,
		stats.VectorHits, stats.KeywordHits, stats.Fused, stats.Reranked, kept,
		!failed && kept == 0, degraded, duration.Milliseconds()); err != nil {
		logging.App().Warn("retrieval_trace_write_failed", "error", err)
	}
}

func (s *Server) retrievalTraces(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	rows, err := s.db.Query(c, `SELECT query,doc_count,vector_hits,keyword_hits,fused,reranked,kept,
			no_evidence,degraded,duration_ms,created_at
		FROM retrieval_traces ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()

	out := []gin.H{}
	for rows.Next() {
		var query, degraded string
		var docCount, vectorHits, keywordHits, fused, reranked, kept, durationMS int
		var noEvidence bool
		var at time.Time
		if err := rows.Scan(&query, &docCount, &vectorHits, &keywordHits, &fused, &reranked, &kept,
			&noEvidence, &degraded, &durationMS, &at); err != nil {
			fail(c, err)
			return
		}
		out = append(out, gin.H{
			"query": query, "doc_count": docCount,
			"vector_hits": vectorHits, "keyword_hits": keywordHits,
			"fused": fused, "reranked": reranked, "kept": kept,
			"no_evidence": noEvidence, "degraded": degraded,
			"duration_ms": durationMS, "created_at": at,
		})
	}
	c.JSON(http.StatusOK, gin.H{"traces": out})
}
