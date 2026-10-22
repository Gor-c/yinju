package vectorstore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type chromaRecorder struct {
	upserts []map[string]any
	queries []map[string]any
	deletes []map[string]any
	getIDs  []string
}

func newFakeChroma(t *testing.T, rec *chromaRecorder) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(path, "/collections"):
			io.WriteString(w, `{"id":"col-1","name":"knowledge_base"}`)
		case strings.HasSuffix(path, "/upsert"):
			rec.upserts = append(rec.upserts, body)
			io.WriteString(w, `{}`)
		case strings.HasSuffix(path, "/query"):
			rec.queries = append(rec.queries, body)
			io.WriteString(w, `{"documents":[["内容A","内容B"]],"distances":[[0.1,0.2]],"metadatas":[[{"doc_id":"doc-1","filename":"制度.docx","doc_type":"制度"},{"doc_id":"doc-2","filename":"预案.pdf","doc_type":"预案"}]]}`)
		case strings.HasSuffix(path, "/get"):
			io.WriteString(w, `{"ids":["a","b","c"]}`)
		case strings.HasSuffix(path, "/delete"):
			rec.deletes = append(rec.deletes, body)
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected Chroma path %s", path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestChromaUpsertSearchCountDelete(t *testing.T) {
	rec := &chromaRecorder{}
	srv := newFakeChroma(t, rec)
	t.Setenv("CHROMA_URL", srv.URL)
	t.Setenv("CHROMA_COLLECTION", "knowledge_base")

	store, err := NewChromaFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	vec := make([]float64, 1024)
	vec[0] = 1
	if err := store.Upsert(ctx, []Chunk{
		{DocID: "doc-1", DocType: "制度", Filename: "制度.docx", Index: 0, Content: "内容A", Embedding: vec},
		{DocID: "doc-1", DocType: "制度", Filename: "制度.docx", Index: 1, Content: "内容B", Embedding: vec},
	}); err != nil {
		t.Fatal(err)
	}
	if len(rec.upserts) != 1 {
		t.Fatalf("expected one upsert request, got %d", len(rec.upserts))
	}
	ids, _ := rec.upserts[0]["ids"].([]any)
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("unexpected ids: %#v", rec.upserts[0]["ids"])
	}
	metas, _ := rec.upserts[0]["metadatas"].([]any)
	if len(metas) != 2 {
		t.Fatalf("unexpected metadatas: %#v", rec.upserts[0]["metadatas"])
	}
	meta, _ := metas[0].(map[string]any)
	if meta["doc_id"] != "doc-1" || meta["filename"] != "制度.docx" || meta["chunk_index"] != float64(0) {
		t.Fatalf("unexpected metadata: %#v", meta)
	}

	matches, err := store.Search(ctx, []string{"doc-1", "doc-2"}, vec, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Filename != "制度.docx" || matches[1].DocType != "预案" {
		t.Fatalf("unexpected matches: %#v", matches)
	}
	if matches[0].DocID != "doc-1" || matches[0].Distance != 0.1 {
		t.Fatalf("unexpected first match: %#v", matches[0])
	}
	query := rec.queries[0]
	where, _ := query["where"].(map[string]any)
	docFilter, _ := where["doc_id"].(map[string]any)
	in, _ := docFilter["$in"].([]any)
	if len(in) != 2 || in[0] != "doc-1" {
		t.Fatalf("search is not scoped to documents: %#v", where)
	}

	count, err := store.Count(ctx, []string{"doc-1"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("Count() = %d, want 3", count)
	}

	if err := store.DeleteFrom(ctx, "doc-1", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFrom(ctx, "doc-1", 4); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDoc(ctx, "doc-2"); err != nil {
		t.Fatal(err)
	}
	if len(rec.deletes) != 3 {
		t.Fatalf("expected three delete requests, got %d", len(rec.deletes))
	}
	first, _ := rec.deletes[0]["where"].(map[string]any)
	if first["doc_id"] != "doc-1" {
		t.Fatalf("full delete should filter by doc_id: %#v", first)
	}
	second, _ := rec.deletes[1]["where"].(map[string]any)
	if clauses, _ := second["$and"].([]any); len(clauses) != 2 {
		t.Fatalf("tail delete should also filter chunk_index: %#v", second)
	}
	third, _ := rec.deletes[2]["where"].(map[string]any)
	if third["doc_id"] != "doc-2" {
		t.Fatalf("DeleteDoc should filter by doc_id: %#v", third)
	}
}

func TestChromaRejectsWrongDimensions(t *testing.T) {
	rec := &chromaRecorder{}
	srv := newFakeChroma(t, rec)
	t.Setenv("CHROMA_URL", srv.URL)
	store, err := NewChromaFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Upsert(ctx, []Chunk{{DocID: "doc-1", Filename: "a.txt", Embedding: []float64{1, 2}}}); err == nil {
		t.Fatal("expected upsert dimension error")
	}
	if _, err := store.Search(ctx, []string{"doc-1"}, []float64{1, 2}, 3); err == nil {
		t.Fatal("expected search dimension error")
	}
	if _, err := store.Search(ctx, []string{"doc-1"}, make([]float64, 1024), 0); err != nil {
		t.Fatalf("limit 0 should return no results without error: %v", err)
	}
	if _, err := store.Search(ctx, nil, make([]float64, 1024), 5); err != nil {
		t.Fatalf("empty doc list should return no results without error: %v", err)
	}
}

func TestChromaChunksSortsByIndex(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/collections"):
			io.WriteString(w, `{"id":"col-1","name":"knowledge_base"}`)
		case strings.HasSuffix(r.URL.Path, "/get"):
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			// Chroma 返回顺序不保证，这里故意倒序。
			io.WriteString(w, `{"documents":["第二块","第一块"],"metadatas":[{"doc_id":"doc-1","doc_type":"预案","chunk_index":1},{"doc_id":"doc-1","doc_type":"预案","chunk_index":0}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Setenv("CHROMA_URL", srv.URL)
	store, err := NewChromaFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := store.Chunks(context.Background(), "doc-1", 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if chunks[0].Index != 0 || chunks[0].Content != "第一块" {
		t.Fatalf("chunks not sorted by index: %#v", chunks)
	}
	if chunks[1].Index != 1 || chunks[1].DocType != "预案" {
		t.Fatalf("unexpected second chunk: %#v", chunks[1])
	}
	if gotBody["limit"] != float64(10) {
		t.Fatalf("limit not forwarded: %#v", gotBody)
	}
	// Paging is expressed as a chunk_index range so results stay stable even
	// though Chroma does not order /get results.
	where, ok := gotBody["where"].(map[string]any)
	if !ok {
		t.Fatalf("where clause missing: %#v", gotBody)
	}
	var gte, lt, scoped bool
	if filters, ok := where["$and"].([]any); ok {
		for _, filter := range filters {
			field, _ := filter.(map[string]any)
			if field["doc_id"] == "doc-1" {
				scoped = true
			}
			index, _ := field["chunk_index"].(map[string]any)
			if v, ok := index["$gte"].(float64); ok && v == 20 {
				gte = true
			}
			if v, ok := index["$lt"].(float64); ok && v == 30 {
				lt = true
			}
		}
	}
	if !gte || !lt || !scoped {
		t.Fatalf("chunk_index range or doc scope not forwarded: %#v", gotBody)
	}
}
