package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIStreamWithToolCalls(t *testing.T) {
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"delta":{"content":"你"}}]}`,
			`{"choices":[{"delta":{"content":"好"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"vector_search","arguments":"{\"query\":\"知识"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"库\"}"}}]}}]}`,
			`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL, "test-key", "test-model", srv.Client())
	var deltas strings.Builder
	message, usage, err := client.Complete(context.Background(), []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "用户"},
	}, []ToolDef{{Name: "vector_search", Description: "search", Parameters: map[string]any{"type": "object"}}}, func(delta string) {
		deltas.WriteString(delta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if deltas.String() != "你好" || message.Content != "你好" {
		t.Fatalf("content = %q, deltas = %q", message.Content, deltas.String())
	}
	if len(message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %#v", message.ToolCalls)
	}
	if message.ToolCalls[0].ID != "call_1" || message.ToolCalls[0].Name != "vector_search" {
		t.Fatalf("tool call = %#v", message.ToolCalls[0])
	}
	if message.ToolCalls[0].Arguments != `{"query":"知识库"}` {
		t.Fatalf("arguments = %q", message.ToolCalls[0].Arguments)
	}
	if usage.TotalTokens != 14 {
		t.Fatalf("usage = %#v", usage)
	}
	if received["stream"] != true {
		t.Fatalf("stream flag = %#v", received["stream"])
	}
	if _, ok := received["tools"]; !ok {
		t.Fatalf("tools were not sent: %#v", received)
	}
}

func TestOpenAIStreamRecordsFinishReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"vector_search","arguments":"{\"query\":\"知"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"finish_reason":"length"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL, "test-key", "test-model", srv.Client())
	message, _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message.FinishReason != "length" {
		t.Fatalf("finish reason = %q, want length", message.FinishReason)
	}
}

func TestOpenAIFallsBackToNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["stream"] == true {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"streaming tool calls are not supported"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"fallback answer"}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL, "key", "model", srv.Client())
	message, usage, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}},
		[]ToolDef{{Name: "t", Parameters: map[string]any{"type": "object"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "fallback answer" || usage.TotalTokens != 5 {
		t.Fatalf("message = %#v, usage = %#v", message, usage)
	}
}

func TestOpenAIReportsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()
	client := NewOpenAIClient(srv.URL, "key", "model", srv.Client())
	if _, _, err := client.Complete(context.Background(), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenAINonStreamJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"plain"}}]}`)
	}))
	defer srv.Close()
	client := NewOpenAIClient(srv.URL, "key", "model", srv.Client())
	message, _, err := client.Complete(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "plain" {
		t.Fatalf("content = %q", message.Content)
	}
}

func TestToOpenAIMessages(t *testing.T) {
	converted := toOpenAIMessages([]Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "tool", Arguments: `{"a":1}`}}},
		{Role: "tool", ToolCallID: "call_1", Content: `{"ok":true}`},
	})
	if len(converted) != 2 {
		t.Fatalf("len = %d", len(converted))
	}
	if converted[0]["content"] != nil {
		t.Fatalf("assistant content should be null: %#v", converted[0])
	}
	calls, ok := converted[0]["tool_calls"].([]map[string]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("tool_calls = %#v", converted[0]["tool_calls"])
	}
	if converted[1]["tool_call_id"] != "call_1" {
		t.Fatalf("tool message = %#v", converted[1])
	}
}
