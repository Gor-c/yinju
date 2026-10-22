package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func doRequest(r http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func testServer() *Server {
	return &Server{http: &http.Client{Timeout: 5 * time.Second}}
}

func testRouter(s *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return s.routes()
}

func TestJobSnapshotAndJSON(t *testing.T) {
	j := &Job{ID: "job-1", UnitID: "u1", DocType: "制度", Filename: "预案.pdf", Status: "queued", Message: "等待索引"}
	j.set("running", 60, "生成向量")
	j.setChunks(12)

	view := j.snapshot()
	if view.Status != "running" || view.Progress != 60 || view.Chunks != 12 {
		t.Fatalf("unexpected snapshot: %#v", view)
	}
	if view.UnitID != "u1" || view.Filename != "预案.pdf" || view.DocType != "制度" {
		t.Fatalf("snapshot lost document fields: %#v", view)
	}

	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"job_id", "unit_id", "doc_type", "filename", "status", "progress", "message", "chunks"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("marshaled job is missing %q: %s", key, raw)
		}
	}
}

func TestFailJobWithoutDatabase(t *testing.T) {
	s := testServer()
	j := &Job{ID: "job-2", Status: "running", Progress: 30, Message: "生成向量"}
	s.failJob(j, "模型服务失败")
	view := j.snapshot()
	if view.Status != "failed" || view.Message != "模型服务失败" {
		t.Fatalf("unexpected failed job: %#v", view)
	}
	if view.Progress != 30 {
		t.Fatalf("failure should keep progress, got %d", view.Progress)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	s := testServer()
	r := gin.New()
	r.Use(s.requestIDMiddleware)
	r.GET("/ok", func(c *gin.Context) { c.JSON(200, gin.H{"id": c.GetString(requestIDKey)}) })

	w := doRequest(r, http.MethodGet, "/ok", "", nil)
	generated := w.Header().Get("X-Request-ID")
	if generated == "" || len(generated) != 16 {
		t.Fatalf("generated request id = %q, want 16 hex chars", generated)
	}

	w = doRequest(r, http.MethodGet, "/ok", "", map[string]string{"X-Request-ID": "upstream-id"})
	if got := w.Header().Get("X-Request-ID"); got != "upstream-id" {
		t.Fatalf("request id = %q, want upstream-id", got)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	s := testServer()
	r := gin.New()
	r.Use(s.requestIDMiddleware, s.recoveryMiddleware)
	r.GET("/panic", func(c *gin.Context) { panic("boom") })

	w := doRequest(r, http.MethodGet, "/panic", "", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "internal server error") {
		t.Fatalf("body = %s", w.Body.String())
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("panic response lost the request id header")
	}
}

func TestJobEndpointUnknownWithoutDatabase(t *testing.T) {
	s := testServer()
	r := testRouter(s)
	w := doRequest(r, http.MethodGet, "/api/v1/jobs/missing", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestMain(m *testing.M) {
	// 重试退避在测试里没有意义，调小避免拖慢用例。
	modelRetryBase = time.Millisecond
	os.Exit(m.Run())
}
