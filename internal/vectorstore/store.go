// Package vectorstore isolates vector persistence and similarity search from
// the knowledge base API and document pipeline.
package vectorstore

import "context"

// Chunk contains the document fields required for indexing and citations.
// 文档是独立实体（DocID），知识库只是通过 document_units 关联的标签，
// 因此向量只存一份，可被多个知识库共用。
type Chunk struct {
	DocID     string
	Filename  string
	DocType   string
	Index     int
	Content   string
	Embedding []float64
}

// Match is a retrieved chunk with its vector distance from the query.
type Match struct {
	DocID    string
	Filename string
	DocType  string
	Content  string
	Distance float64
}

// Store is the replacement boundary for pgvector, Chroma, or another vector DB.
// Implementations own vector serialization, metadata filtering, and index writes.
//
// 所有方法以 docID 为单位：调用方先用 document_units 把「知识库」翻译成
// doc_id 集合，再检索，这样一份文档可以被多个知识库共享。
type Store interface {
	Upsert(ctx context.Context, chunks []Chunk) error
	Search(ctx context.Context, docIDs []string, embedding []float64, limit int) ([]Match, error)
	Count(ctx context.Context, docIDs []string) (int, error)
	// DeleteFrom removes stored chunks of one document whose chunk index is at
	// least fromIndex. Passing 0 deletes the whole document's vectors.
	DeleteFrom(ctx context.Context, docID string, fromIndex int) error
	// DeleteDoc removes every chunk of a document.
	DeleteDoc(ctx context.Context, docID string) error
	// Chunks returns one document's chunks ordered by chunk index, used to
	// preview what was actually indexed.
	Chunks(ctx context.Context, docID string, limit, offset int) ([]Chunk, error)
	// Reset drops all stored vectors, used when the index schema changes and
	// documents must be re-indexed.
	Reset(ctx context.Context) error
}
