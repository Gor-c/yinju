package app

// 关键词镜像表：混合检索的关键词通道落在 PostgreSQL 的 pg_trgm 上，
// 而不是向量库自带的全文检索。原因有两个：
//  1. 换向量库（pgvector ↔ Chroma）不应该改变关键词召回的行为；
//  2. 中文没有空格分词，trigram 的字符三联组比按词切分更稳。
//
// 这一层只负责「存片段原文 / 查片段原文」，不参与融合与排序——
// 融合、重排、降级阈值全部在 internal/rag 里。

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/vectorstore"
)

// keywordMinWordSimilarity 是 trigram 相似度的入口下限，只用于「粗筛候选」。
// 实测相关片段在 0.27–0.44、无关片段接近 0，因此 0.12 足够宽松，
// 不会漏掉召回；真正的取舍由重排阈值和覆盖度闸门负责。
const (
	keywordMinWordSimilarity = 0.12
	chunkTextBatchLimit      = 200
)

// ensureChunkTextSchema 建关键词索引表。pg_trgm 需要超级用户权限，
// 创建失败时关键词召回自动退化为 LIKE 计数，不影响主流程。
func (s *Server) ensureChunkTextSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm`); err != nil {
		logging.App().Warn("pg_trgm_unavailable", "error", err)
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chunk_text (
			doc_id text NOT NULL,
			chunk_index integer NOT NULL,
			content text NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (doc_id, chunk_index)
		)`,
		`CREATE INDEX IF NOT EXISTS chunk_text_doc_idx ON chunk_text (doc_id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("chunk_text_schema_failed", "error", err)
			return
		}
	}
	// trigram 索引需要 pg_trgm，失败不阻塞建表。
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS chunk_text_trgm_idx ON chunk_text USING gin (content gin_trgm_ops)`); err != nil {
		logging.App().Warn("chunk_text_trgm_index_failed", "error", err)
	}
}

// syncChunkText 把一批片段写入关键词镜像，与向量写入保持同批次。
func (s *Server) syncChunkText(ctx context.Context, docID string, chunks []vectorstore.Chunk) error {
	if s.db == nil || docID == "" || len(chunks) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, chunk := range chunks {
		batch.Queue(`INSERT INTO chunk_text(doc_id,chunk_index,content,updated_at)
			VALUES($1,$2,$3,now())
			ON CONFLICT(doc_id,chunk_index) DO UPDATE SET content=EXCLUDED.content, updated_at=now()`,
			docID, chunk.Index, chunk.Content)
	}
	results := s.db.SendBatch(ctx, batch)
	defer results.Close()
	for range chunks {
		if _, err := results.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) deleteChunkTextFrom(ctx context.Context, docID string, fromIndex int) error {
	if s.db == nil || docID == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `DELETE FROM chunk_text WHERE doc_id=$1 AND chunk_index >= $2`, docID, fromIndex)
	return err
}

func (s *Server) deleteChunkText(ctx context.Context, docID string) error {
	return s.deleteChunkTextFrom(ctx, docID, 0)
}

// backfillChunkText 在关键词镜像为空但已有文档时，从向量库回填一次，
// 让老数据无需重建索引也能用上混合检索。
func (s *Server) backfillChunkText(ctx context.Context) {
	if s.db == nil || s.vectors == nil {
		return
	}
	// 只补缺失的文档：某次写入失败（如数据库抖动）时不会因为表非空而永久缺索引。
	rows, err := s.db.Query(ctx, `SELECT d.doc_id FROM documents d
		WHERE d.chunk_count > 0
		  AND NOT EXISTS (SELECT 1 FROM chunk_text ct WHERE ct.doc_id = d.doc_id)`)
	if err != nil {
		logging.App().Warn("chunk_text_backfill_query_failed", "error", err)
		return
	}
	docIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			docIDs = append(docIDs, id)
		}
	}
	rows.Close()

	total := 0
	for _, docID := range docIDs {
		for offset := 0; ; offset += chunkTextBatchLimit {
			chunks, err := s.vectors.Chunks(ctx, docID, chunkTextBatchLimit, offset)
			if err != nil {
				logging.App().Warn("chunk_text_backfill_failed", "doc_id", docID, "error", err)
				break
			}
			if len(chunks) == 0 {
				break
			}
			if err := s.syncChunkText(ctx, docID, chunks); err != nil {
				logging.App().Warn("chunk_text_backfill_write_failed", "doc_id", docID, "error", err)
				break
			}
			total += len(chunks)
			if len(chunks) < chunkTextBatchLimit {
				break
			}
		}
	}
	if total > 0 {
		logging.App().Info("chunk_text_backfilled", "documents", len(docIDs), "chunks", total)
	}
}

// keywordSearch 实现 rag 的关键词通道：用 trigram 相似度做中文关键词召回。
// 它不排序、不融合、不做阈值判定——只返回候选和相似度分数。
func (s *Server) keywordSearch(ctx context.Context, docIDs []string, query string, limit int) ([]vectorstore.Match, error) {
	if s.db == nil || len(docIDs) == 0 || limit < 1 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT ct.doc_id, COALESCE(d.filename,''), COALESCE(d.doc_type,''),
		ct.chunk_index, ct.content, word_similarity($2, ct.content) AS score
		FROM chunk_text ct
		JOIN documents d ON d.doc_id = ct.doc_id
		WHERE ct.doc_id = ANY($1) AND word_similarity($2, ct.content) >= $3
		ORDER BY score DESC LIMIT $4`, docIDs, query, keywordMinWordSimilarity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]vectorstore.Match, 0, limit)
	for rows.Next() {
		var match vectorstore.Match
		var score float64
		if err := rows.Scan(&match.DocID, &match.Filename, &match.DocType,
			&match.ChunkIndex, &match.Content, &score); err != nil {
			return nil, err
		}
		match.Score = score
		out = append(out, match)
	}
	return out, rows.Err()
}

// 以下文案是「无依据」出口，属于 prompt 契约而不是检索逻辑，
// 因此留在 app 层：改措辞不该碰检索代码。

// noEvidenceNote 是给模型的指令性说明（工具返回值里使用）。
const noEvidenceNote = "知识库中没有检索到与该问题相关的依据。请如实告知用户未找到相关内容，" +
	"并说明可以尝试的提问方向；不要凭常识或猜测补充答案。"

// noEvidenceAnswer 是直接返回给用户看的文案，不含对模型的指令。
const noEvidenceAnswer = "知识库中没有检索到与该问题相关的依据。可以换一种说法重新提问，" +
	"或确认相关文档已经上传并完成索引。"

func emptyResultNote(query string) string {
	return fmt.Sprintf("%s（检索词：%s）", noEvidenceNote, truncateRunes(query, 60))
}
