package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"knowledge-base/internal/websearch"
)

func TestEmbeddingRequestPreservesIndexOrder(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var payload struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		gotModel = payload.Model
		io.WriteString(w, `{"data":[{"index":1,"embedding":[2,2]},{"index":0,"embedding":[1,1]}]}`)
	}))
	defer srv.Close()
	t.Setenv("EMBED_BASE_URL", srv.URL+"/")
	t.Setenv("EMBED_API_KEY", "embed-key")
	t.Setenv("EMBED_MODEL", "test-embed")

	s := &Server{http: srv.Client()}
	vecs, err := s.embed([]string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len(vecs) = %d, want 2", len(vecs))
	}
	if len(vecs[0]) == 0 || vecs[0][0] != 1 || vecs[1][0] != 2 {
		t.Fatalf("index order was not preserved: %#v", vecs)
	}
	if gotAuth != "Bearer embed-key" {
		t.Fatalf("authorization header = %q", gotAuth)
	}
	if gotModel != "test-embed" {
		t.Fatalf("model = %q", gotModel)
	}
}

func TestEmbeddingRequestReportsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":"rate limited"}`)
	}))
	defer srv.Close()
	t.Setenv("EMBED_BASE_URL", srv.URL)
	t.Setenv("EMBED_API_KEY", "embed-key")

	s := &Server{http: srv.Client()}
	if _, err := s.embedContext(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("expected 429 error, got %v", err)
	}
}

func TestEmbeddingRequiresConfiguration(t *testing.T) {
	t.Setenv("EMBED_BASE_URL", "")
	t.Setenv("EMBED_API_KEY", "")
	s := &Server{http: http.DefaultClient}
	if _, err := s.embedContext(context.Background(), []string{"a"}); err == nil {
		t.Fatal("expected configuration error")
	}
}

func fakeLLM(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if _, ok := payload["messages"]; !ok {
			t.Error("chat payload has no messages")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": answer}}},
		})
	}))
}

func configureLLM(t *testing.T, url string) {
	t.Helper()
	t.Setenv("LLM_BASE_URL", url)
	t.Setenv("LLM_API_KEY", "llm-key")
	t.Setenv("LLM_MODEL", "test-chat")
}

func TestChatWithoutUnitUsesConfiguredSkills(t *testing.T) {
	srv := fakeLLM(t, "这是回答")
	defer srv.Close()
	configureLLM(t, srv.URL)
	t.Setenv("AGENT_SKILLS_JSON", `[{"id":"custom-search","name":"自定义检索","instructions":"引用文件名"}]`)

	r := testRouter(&Server{http: srv.Client()})
	w := doRequest(r, http.MethodPost, "/api/v1/agent/chat", `{"message":"该项目有什么隐患？","expert":"kb-expert"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Answer    string     `json:"answer"`
		Expert    string     `json:"expert"`
		Skills    []string   `json:"skills"`
		Citations []citation `json:"citations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Answer != "这是回答" || out.Expert != "kb-expert" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if len(out.Skills) != 1 || out.Skills[0] != "custom-search" {
		t.Fatalf("skills = %#v, want configured skill id", out.Skills)
	}
	if out.Citations == nil {
		t.Fatalf("citations should be an empty array, got %s", w.Body.String())
	}
}

func TestChatFiltersRequestedSkills(t *testing.T) {
	srv := fakeLLM(t, "answer")
	defer srv.Close()
	configureLLM(t, srv.URL)
	t.Setenv("AGENT_SKILLS_JSON", `[{"id":"a","name":"技能A"},{"id":"b","name":"技能B"}]`)

	r := testRouter(&Server{http: srv.Client()})
	w := doRequest(r, http.MethodPost, "/api/v1/agent/chat", `{"message":"你好","skills":["b"]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var out struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Skills) != 1 || out.Skills[0] != "b" {
		t.Fatalf("skills = %#v, want [b]", out.Skills)
	}
}

func TestChatWithUnitRequiresDatabase(t *testing.T) {
	srv := fakeLLM(t, "answer")
	defer srv.Close()
	configureLLM(t, srv.URL)

	r := testRouter(&Server{http: srv.Client()})
	w := doRequest(r, http.MethodPost, "/api/v1/agent/chat", `{"message":"你好","unit_id":"u1"}`, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", w.Code, w.Body.String())
	}
}

func TestChatReportsMissingModelConfig(t *testing.T) {
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_API_KEY", "")
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/agent/chat", `{"message":"你好"}`, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "LLM_BASE_URL") {
		t.Fatalf("response is not actionable: %s", w.Body.String())
	}
}

func TestChatRequiresMessage(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/agent/chat", `{}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAgentStreamWithoutUnit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"流式"}}]}`+"\n\n")
		io.WriteString(w, `data: {"choices":[{"delta":{"content":"回答"}}]}`+"\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	configureLLM(t, srv.URL)
	t.Setenv("AGENT_SKILLS_JSON", "")

	r := testRouter(&Server{http: srv.Client()})
	w := doRequest(r, http.MethodPost, "/api/v1/agent/stream", `{"message":"你好"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content type = %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"event: run_started", "event: message_delta", "event: run_completed", "流式", "回答"} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE body is missing %q:\n%s", want, body)
		}
	}
}

func TestAgentStreamValidatesMessage(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/agent/stream", `{}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAgentStreamUsesWebSearch(t *testing.T) {
	// A fake model that asks for one web_search call, then answers.
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		hasTool := false
		for _, msg := range body.Messages {
			if msg.Role == "tool" {
				hasTool = true
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if hasTool {
			w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","content":"已联网检索到网页。"}}]}` + "\n\n"))
		} else {
			w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"消防规范 2026\"}"}}]}}]}` + "\n\n"))
		}
		w.Write([]byte(`data: {"choices":[{"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer llm.Close()

	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"results":[{"title":"消防安全规范","url":"https://gov.example.cn/fire","content":"公开信息"}]}`))
	}))
	defer search.Close()

	web, err := websearch.New(websearch.Config{Enabled: true, Provider: "tavily", APIKey: "k", BaseURL: search.URL}, search.Client())
	if err != nil {
		t.Fatal(err)
	}
	configureLLM(t, llm.URL)
	t.Setenv("AGENT_SKILLS_JSON", "")

	s := testServer()
	s.web = web
	w := doRequest(testRouter(s), http.MethodPost, "/api/v1/agent/stream", `{"message":"2026 消防规范","web_search":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"event: tool_call", `"tool":"web_search"`, `"source_type":"web"`, "已联网检索到网页"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}

func TestAgentStreamSkipsWebSearchWhenNotRequested(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"消防规范\"}"}}]}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer llm.Close()

	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("search provider must not be called when web search is off")
		w.Write([]byte(`{"results":[]}`))
	}))
	defer search.Close()

	web, err := websearch.New(websearch.Config{Enabled: true, Provider: "tavily", APIKey: "k", BaseURL: search.URL}, search.Client())
	if err != nil {
		t.Fatal(err)
	}
	configureLLM(t, llm.URL)
	t.Setenv("AGENT_SKILLS_JSON", "")

	s := testServer()
	s.web = web
	w := doRequest(testRouter(s), http.MethodPost, "/api/v1/agent/stream", `{"message":"消防规范"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"source_type":"web"`) {
		t.Fatalf("web search ran without opt-in:\n%s", w.Body.String())
	}
}

func TestWebSearchToolRequiresOptIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"消防规范","url":"https://gov.example.cn/a","content":"摘要"}]}`))
	}))
	defer srv.Close()
	client, err := websearch.New(websearch.Config{Enabled: true, Provider: "tavily", APIKey: "k", BaseURL: srv.URL}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}

	s := testServer()
	if reg := s.agentRegistry(context.Background(), []string{"unit-1"}, nil, nil, nil, true); contains(reg.Names(), "web_search") {
		t.Fatal("web_search registered even though the server has no search client")
	}

	s.web = client
	off := s.agentRegistry(context.Background(), []string{"unit-1"}, nil, nil, nil, false)
	if contains(off.Names(), "web_search") {
		t.Fatalf("web_search registered without the per-request switch: %#v", off.Names())
	}

	collector := &citationCollector{}
	on := s.agentRegistry(context.Background(), []string{"unit-1"}, nil, collector, nil, true)
	tool, ok := on.Get("web_search")
	if !ok {
		t.Fatalf("web_search not registered: %#v", on.Names())
	}
	data, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"消防规范"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := data.(map[string]any)
	if payload["source"] != "web" || payload["notice"] == nil {
		t.Fatalf("result is missing the untrusted-content notice: %#v", payload)
	}
	citations := collector.snapshot()
	if len(citations) != 1 || citations[0].SourceType != "web" || citations[0].URL != "https://gov.example.cn/a" {
		t.Fatalf("citations = %#v", citations)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestAgentRegistryIsUnitScoped(t *testing.T) {
	s := testServer()
	registry := s.agentRegistry(context.Background(), []string{"unit-1"}, map[string]string{"unit-1": "一号项目"}, nil, nil, false)
	names := registry.Names()
	want := []string{"get_unit_profile", "query_hazards", "audit_history", "vector_search"}
	if len(names) != len(want) {
		t.Fatalf("tools = %#v", names)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("tool[%d] = %q, want %q", i, names[i], name)
		}
	}
	// unit_id 可以出现，但必须限定在用户已选项目内，模型无法跨项目查询。
	for _, def := range registry.Definitions() {
		properties, _ := def.Parameters["properties"].(map[string]any)
		unitProp, ok := properties["unit_id"].(map[string]any)
		if !ok {
			t.Fatalf("tool %s should expose a scoped unit_id", def.Name)
		}
		enum, _ := unitProp["enum"].([]string)
		if len(enum) != 1 || enum[0] != "unit-1" {
			t.Fatalf("tool %s scope = %#v", def.Name, enum)
		}
	}
}

func TestAskRequiresDatabase(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/ask", `{"unit_id":"u1","question":"问题"}`, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestAskValidatesInput(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/ask", `{"unit_id":"u1"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAskRejectsLongQuestion(t *testing.T) {
	r := testRouter(testServer())
	body, err := json.Marshal(map[string]string{
		"unit_id":  "u1",
		"question": strings.Repeat("问", maxQuestionRunes+1),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := doRequest(r, http.MethodPost, "/api/v1/ask", string(body), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("知识库档案", 3); got != "知识库" {
		t.Fatalf("truncateRunes() = %q", got)
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Fatalf("truncateRunes() = %q", got)
	}
	if got := truncateRunes("abc", 0); got != "" {
		t.Fatalf("truncateRunes(0) = %q", got)
	}
}

func TestUploadWithoutDatabase(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodPost, "/api/v1/upload", `x`, nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestAgentConfigDefaults(t *testing.T) {
	t.Setenv("AGENT_EXPERTS_JSON", "")
	t.Setenv("AGENT_SKILLS_JSON", "")
	t.Setenv("MCP_CONNECTORS_JSON", "")
	r := testRouter(testServer())
	w := doRequest(r, http.MethodGet, "/api/v1/agent/config", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var out struct {
		Experts    []expert    `json:"experts"`
		Skills     []skill     `json:"skills"`
		Connectors []connector `json:"connectors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Experts) == 0 || len(out.Skills) == 0 {
		t.Fatalf("expected built-in experts and skills: %s", w.Body.String())
	}
	if out.Connectors == nil {
		t.Fatalf("connectors should be an empty array: %s", w.Body.String())
	}
}

func TestHealthWithoutDatabase(t *testing.T) {
	r := testRouter(testServer())
	w := doRequest(r, http.MethodGet, "/health", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"database":false`) {
		t.Fatalf("health = %d %s", w.Code, w.Body.String())
	}
}
