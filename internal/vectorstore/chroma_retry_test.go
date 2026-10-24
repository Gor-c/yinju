package vectorstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 复现真实故障：Chroma 端集合被重建后 ID 变化，进程还在用旧 ID。
// 修复前这里会一直 404 直到重启；修复后应自动重新解析并重试一次。
func TestChromaRecoversFromStaleCollectionID(t *testing.T) {
	var reread int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/tenants/default_tenant/databases/default_database/collections":
			reread++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"fresh-id"}`))
		case strings.Contains(r.URL.Path, "/collections/stale-id/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"NotFoundError","message":"Collection [stale-id] does not exist"}`))
		case strings.Contains(r.URL.Path, "/collections/fresh-id/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	store := &ChromaStore{
		baseURL:      server.URL,
		tenant:       "default_tenant",
		database:     "default_database",
		name:         "knowledge_base",
		collectionID: "stale-id",
		client:       server.Client(),
	}

	err := store.request(context.Background(), http.MethodPost, store.collectionPath()+"/delete", map[string]any{}, nil)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if store.collectionID != "fresh-id" {
		t.Fatalf("collection id should be refreshed, got %q", store.collectionID)
	}
	if reread != 1 {
		t.Fatalf("expected exactly one collection re-resolve, got %d", reread)
	}
}

// 非 404 的错误不能被吞掉，也不能触发重试。
func TestChromaDoesNotRetryOnOtherErrors(t *testing.T) {
	var reread int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/collections") {
			reread++
			_, _ = w.Write([]byte(`{"id":"fresh-id"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	store := &ChromaStore{
		baseURL:      server.URL,
		tenant:       "default_tenant",
		database:     "default_database",
		name:         "knowledge_base",
		collectionID: "stale-id",
		client:       server.Client(),
	}

	if err := store.request(context.Background(), http.MethodPost, store.collectionPath()+"/delete", map[string]any{}, nil); err == nil {
		t.Fatal("expected 500 to surface as an error")
	}
	if reread != 0 {
		t.Fatalf("500 must not trigger collection re-resolve, got %d", reread)
	}
}
