package vectorstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	defaultChromaTenant     = "default_tenant"
	defaultChromaDatabase   = "default_database"
	defaultChromaCollection = "knowledge_base"
)

// ChromaStore implements Store over Chroma's v2 REST API.
type ChromaStore struct {
	baseURL      string
	tenant       string
	database     string
	name         string
	client       *http.Client
	collectionID string
}

// NewChromaFromEnv connects to Chroma and creates the configured collection if
// it does not exist. Chroma performs similarity search using supplied vectors;
// embeddings remain owned by the application so all providers share one model.
func NewChromaFromEnv() (Store, error) {
	s := &ChromaStore{
		baseURL:  strings.TrimRight(envValue("CHROMA_URL", "http://localhost:8000"), "/"),
		tenant:   envValue("CHROMA_TENANT", defaultChromaTenant),
		database: envValue("CHROMA_DATABASE", defaultChromaDatabase),
		name:     envValue("CHROMA_COLLECTION", defaultChromaCollection),
		client:   &http.Client{Timeout: 10 * time.Second},
	}
	if _, err := url.ParseRequestURI(s.baseURL); err != nil {
		return nil, fmt.Errorf("invalid CHROMA_URL: %w", err)
	}
	if err := s.ensureCollection(context.Background()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ChromaStore) ensureCollection(ctx context.Context) error {
	var result struct {
		ID string `json:"id"`
	}
	err := s.request(ctx, http.MethodPost, s.rootPath()+"/collections", map[string]any{
		"name":          s.name,
		"get_or_create": true,
		"metadata":      map[string]any{"hnsw:space": "cosine", "application": "knowledge-base"},
	}, &result)
	if err != nil {
		return fmt.Errorf("initialize Chroma collection %q: %w", s.name, err)
	}
	if result.ID == "" {
		return fmt.Errorf("Chroma returned an empty collection id")
	}
	s.collectionID = result.ID
	return nil
}

func envValue(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (s *ChromaStore) rootPath() string {
	return "/api/v2/tenants/" + url.PathEscape(s.tenant) + "/databases/" + url.PathEscape(s.database)
}

func (s *ChromaStore) collectionPath() string {
	return s.rootPath() + "/collections/" + url.PathEscape(s.collectionID)
}

// docFilter 生成按文档集合过滤的 where 条件；调用方保证 docIDs 非空。
func docFilter(docIDs []string) map[string]any {
	if len(docIDs) == 1 {
		return map[string]any{"doc_id": docIDs[0]}
	}
	return map[string]any{"doc_id": map[string]any{"$in": docIDs}}
}

func (s *ChromaStore) Upsert(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(chunks))
	documents := make([]string, 0, len(chunks))
	embeddings := make([][]float64, 0, len(chunks))
	metadatas := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		if len(chunk.Embedding) != embeddingDim() {
			return fmt.Errorf("embedding dimension is %d, expected %d", len(chunk.Embedding), embeddingDim())
		}
		// Chunk identity now follows the document, not (unit, filename).
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", chunk.DocID, chunk.Index)))
		ids = append(ids, hex.EncodeToString(sum[:]))
		documents = append(documents, chunk.Content)
		embeddings = append(embeddings, chunk.Embedding)
		metadatas = append(metadatas, map[string]any{
			"doc_id": chunk.DocID, "doc_type": chunk.DocType, "filename": chunk.Filename, "chunk_index": chunk.Index,
		})
	}
	return s.request(ctx, http.MethodPost, s.collectionPath()+"/upsert", map[string]any{
		"ids": ids, "documents": documents, "embeddings": embeddings, "metadatas": metadatas,
	}, nil)
}

func (s *ChromaStore) Search(ctx context.Context, docIDs []string, embedding []float64, limit int) ([]Match, error) {
	if len(embedding) != embeddingDim() {
		return nil, fmt.Errorf("embedding dimension is %d, expected %d", len(embedding), embeddingDim())
	}
	if limit < 1 || len(docIDs) == 0 {
		return []Match{}, nil
	}
	var result struct {
		Documents [][]string         `json:"documents"`
		Distances [][]float64        `json:"distances"`
		Metadatas [][]map[string]any `json:"metadatas"`
	}
	err := s.request(ctx, http.MethodPost, s.collectionPath()+"/query", map[string]any{
		"query_embeddings": [][]float64{embedding}, "n_results": limit,
		"where":   docFilter(docIDs),
		"include": []string{"documents", "distances", "metadatas"},
	}, &result)
	if err != nil {
		return nil, err
	}
	if len(result.Documents) == 0 {
		return []Match{}, nil
	}
	matches := make([]Match, 0, len(result.Documents[0]))
	for i, content := range result.Documents[0] {
		match := Match{Content: content}
		if len(result.Distances) > 0 && i < len(result.Distances[0]) {
			match.Distance = result.Distances[0][i]
		}
		if len(result.Metadatas) > 0 && i < len(result.Metadatas[0]) {
			match.DocID, _ = result.Metadatas[0][i]["doc_id"].(string)
			match.Filename, _ = result.Metadatas[0][i]["filename"].(string)
			match.DocType, _ = result.Metadatas[0][i]["doc_type"].(string)
		}
		matches = append(matches, match)
	}
	return matches, nil
}

func (s *ChromaStore) DeleteFrom(ctx context.Context, docID string, fromIndex int) error {
	if docID == "" {
		return errors.New("docID is required")
	}
	if fromIndex <= 0 {
		// Chroma 单个条件不需要 $and，避免不同版本的运算符数量校验差异。
		return s.request(ctx, http.MethodPost, s.collectionPath()+"/delete", map[string]any{
			"where": map[string]any{"doc_id": docID},
		}, nil)
	}
	return s.request(ctx, http.MethodPost, s.collectionPath()+"/delete", map[string]any{
		"where": map[string]any{"$and": []any{
			map[string]any{"doc_id": docID},
			map[string]any{"chunk_index": map[string]any{"$gte": fromIndex}},
		}},
	}, nil)
}

func (s *ChromaStore) DeleteDoc(ctx context.Context, docID string) error {
	if docID == "" {
		return errors.New("docID is required")
	}
	return s.request(ctx, http.MethodPost, s.collectionPath()+"/delete", map[string]any{"where": map[string]any{"doc_id": docID}}, nil)
}

// Chunks 返回某份文档的分块内容，用于在页面上预览索引结果。
func (s *ChromaStore) Chunks(ctx context.Context, docID string, limit, offset int) ([]Chunk, error) {
	if limit <= 0 || docID == "" {
		return []Chunk{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	var result struct {
		Documents []string         `json:"documents"`
		Metadatas []map[string]any `json:"metadatas"`
	}
	// Paging uses the chunk_index range instead of Chroma's own offset: /get does
	// not guarantee ordering, so an offset would silently skip or repeat chunks.
	// limit is still forwarded so the server caps the response size.
	err := s.request(ctx, http.MethodPost, s.collectionPath()+"/get", map[string]any{
		"where": map[string]any{"$and": []any{
			map[string]any{"doc_id": docID},
			map[string]any{"chunk_index": map[string]any{"$gte": offset}},
			map[string]any{"chunk_index": map[string]any{"$lt": offset + limit}},
		}},
		"limit":   limit,
		"include": []string{"documents", "metadatas"},
	}, &result)
	if err != nil {
		return nil, err
	}
	out := make([]Chunk, 0, len(result.Documents))
	for i, content := range result.Documents {
		c := Chunk{DocID: docID, Content: content}
		if i < len(result.Metadatas) {
			c.Filename, _ = result.Metadatas[i]["filename"].(string)
			c.DocType, _ = result.Metadatas[i]["doc_type"].(string)
			if v, ok := result.Metadatas[i]["chunk_index"].(float64); ok {
				c.Index = int(v)
			}
		}
		out = append(out, c)
	}
	// Chroma 的 get 不保证顺序，按 chunk_index 排序后返回。
	sort.SliceStable(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

func (s *ChromaStore) Count(ctx context.Context, docIDs []string) (int, error) {
	if len(docIDs) == 0 {
		return 0, nil
	}
	const pageSize = 1000
	count := 0
	for offset := 0; ; offset += pageSize {
		var result struct {
			IDs []string `json:"ids"`
		}
		err := s.request(ctx, http.MethodPost, s.collectionPath()+"/get", map[string]any{
			"where": docFilter(docIDs), "include": []string{}, "limit": pageSize, "offset": offset,
		}, &result)
		if err != nil {
			return 0, err
		}
		count += len(result.IDs)
		if len(result.IDs) < pageSize {
			return count, nil
		}
	}
}

// Reset 清空集合内的全部向量，用于索引结构变更后的全量重建。
// 这里刻意不删除集合本身：正在运行的服务缓存了 collection id，
// 删掉重建会让它的写入全部 404。按 id 分批删除可保持集合 id 不变。
func (s *ChromaStore) Reset(ctx context.Context) error {
	const pageSize = 500
	const maxRounds = 2000
	for round := 0; round < maxRounds; round++ {
		var result struct {
			IDs []string `json:"ids"`
		}
		if err := s.request(ctx, http.MethodPost, s.collectionPath()+"/get", map[string]any{
			"limit": pageSize, "include": []string{},
		}, &result); err != nil {
			return fmt.Errorf("list Chroma chunks: %w", err)
		}
		if len(result.IDs) == 0 {
			return nil
		}
		if err := s.request(ctx, http.MethodPost, s.collectionPath()+"/delete", map[string]any{
			"ids": result.IDs,
		}, nil); err != nil {
			return fmt.Errorf("delete Chroma chunks: %w", err)
		}
	}
	return errors.New("清空向量集合失败：数据量超过预期上限")
}

func (s *ChromaStore) request(ctx context.Context, method, path string, body, output any) error {
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(res.Body); err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Chroma API %s %s returned %d: %s", method, path, res.StatusCode, raw.String())
	}
	if output != nil {
		return json.Unmarshal(raw.Bytes(), output)
	}
	return nil
}
