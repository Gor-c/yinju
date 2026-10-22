-- Optional vector tables for VECTOR_STORE=pgvector.
-- Do not apply this file when using Chroma as the vector provider.
CREATE EXTENSION IF NOT EXISTS vector;

-- 向量以文档归属（doc_id），文件名与类型来自 documents 表，
-- 因此一份文档被多个知识库引用时只存一份向量。
CREATE TABLE IF NOT EXISTS doc_chunks (
  doc_id text NOT NULL,
  chunk_index integer NOT NULL,
  content text NOT NULL,
  embedding vector(1024) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(doc_id, chunk_index)
);
CREATE INDEX IF NOT EXISTS doc_chunks_embedding_idx ON doc_chunks USING hnsw (embedding vector_cosine_ops);
