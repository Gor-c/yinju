package vectorstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGVectorStore stores vectors alongside document metadata in PostgreSQL.
// 向量以 doc_id 归属，文件名与类型来自 documents 表，便于一份文档被多个知识库共享。
type PGVectorStore struct{ db *pgxpool.Pool }

func NewPGVector(db *pgxpool.Pool) *PGVectorStore { return &PGVectorStore{db: db} }

func (s *PGVectorStore) Upsert(ctx context.Context, chunks []Chunk) error {
	for _, chunk := range chunks {
		if len(chunk.Embedding) != embeddingDim() {
			return fmt.Errorf("embedding dimension is %d, expected %d", len(chunk.Embedding), embeddingDim())
		}
		if chunk.DocID == "" {
			return errors.New("docID is required")
		}
		_, err := s.db.Exec(ctx, `INSERT INTO doc_chunks(doc_id,chunk_index,content,embedding)
			VALUES($1,$2,$3,$4::vector)
			ON CONFLICT(doc_id,chunk_index) DO UPDATE
			SET content=EXCLUDED.content,embedding=EXCLUDED.embedding`,
			chunk.DocID, chunk.Index, chunk.Content, vectorLiteral(chunk.Embedding))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *PGVectorStore) Search(ctx context.Context, docIDs []string, embedding []float64, limit int) ([]Match, error) {
	if len(docIDs) == 0 || limit < 1 {
		return []Match{}, nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.doc_id, COALESCE(d.filename,''), COALESCE(d.doc_type,''),
		c.content, c.embedding <=> $1::vector AS distance
		FROM doc_chunks c
		LEFT JOIN documents d ON d.doc_id = c.doc_id
		WHERE c.doc_id = ANY($2)
		ORDER BY c.embedding <=> $1::vector LIMIT $3`, vectorLiteral(embedding), docIDs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := make([]Match, 0, limit)
	for rows.Next() {
		var match Match
		if err := rows.Scan(&match.DocID, &match.Filename, &match.DocType, &match.Content, &match.Distance); err != nil {
			return nil, err
		}
		matches = append(matches, match)
	}
	return matches, rows.Err()
}

func (s *PGVectorStore) DeleteFrom(ctx context.Context, docID string, fromIndex int) error {
	if docID == "" {
		return errors.New("docID is required")
	}
	_, err := s.db.Exec(ctx, `DELETE FROM doc_chunks WHERE doc_id=$1 AND chunk_index >= $2`, docID, fromIndex)
	return err
}

func (s *PGVectorStore) DeleteDoc(ctx context.Context, docID string) error {
	if docID == "" {
		return errors.New("docID is required")
	}
	_, err := s.db.Exec(ctx, `DELETE FROM doc_chunks WHERE doc_id=$1`, docID)
	return err
}

// Chunks 返回某份文档的分块内容，用于在页面上预览索引结果。
func (s *PGVectorStore) Chunks(ctx context.Context, docID string, limit, offset int) ([]Chunk, error) {
	if limit <= 0 || docID == "" {
		return []Chunk{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `SELECT c.chunk_index, c.content, COALESCE(d.filename,''), COALESCE(d.doc_type,'')
		FROM doc_chunks c
		LEFT JOIN documents d ON d.doc_id = c.doc_id
		WHERE c.doc_id=$1 ORDER BY c.chunk_index LIMIT $2 OFFSET $3`,
		docID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chunk{}
	for rows.Next() {
		c := Chunk{DocID: docID}
		if err := rows.Scan(&c.Index, &c.Content, &c.Filename, &c.DocType); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGVectorStore) Count(ctx context.Context, docIDs []string) (int, error) {
	if len(docIDs) == 0 {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(ctx, `SELECT count(*) FROM doc_chunks WHERE doc_id = ANY($1)`, docIDs).Scan(&count)
	return count, err
}

// Reset 清空全部向量，用于索引结构变更后的全量重建。
func (s *PGVectorStore) Reset(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `DELETE FROM doc_chunks`)
	return err
}

func vectorLiteral(values []float64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(value, 'f', -1, 64))
	}
	b.WriteByte(']')
	return b.String()
}
