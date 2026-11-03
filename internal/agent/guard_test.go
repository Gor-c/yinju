package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestActiveDefsFiltersDisabledTool(t *testing.T) {
	defs := []ToolDef{toolDef("a"), toolDef("b")}
	got := activeDefs(defs, map[string]bool{"a": true})
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("expected only b, got %+v", got)
	}
	if len(activeDefs(defs, nil)) != 2 {
		t.Fatal("empty disabled set should keep all tools")
	}
}

func TestEstimateTokensCountsCJKHeavier(t *testing.T) {
	cjk := estimateTokens("知识库")
	ascii := estimateTokens("abc")
	if cjk <= ascii {
		t.Fatalf("CJK text should cost more: cjk=%d ascii=%d", cjk, ascii)
	}
}

func TestCompactMessagesTrimsToolResults(t *testing.T) {
	long := strings.Repeat("片段内容", 2000)
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "tool", ToolCallID: "1", Name: "vector_search", Content: long},
		{Role: "user", Content: "问题"},
	}
	got := compactMessages(messages, 1000)
	if len(got) != len(messages) {
		t.Fatalf("message count must stay stable, got %d", len(got))
	}
	if len([]rune(got[1].Content)) >= len([]rune(long)) {
		t.Fatal("tool result was not compacted")
	}
	if got[0].Content != "sys" || got[2].Content != "问题" {
		t.Fatal("non-tool messages should be untouched when only tool trimming is needed")
	}
}

func TestCompactMessagesNoopUnderBudget(t *testing.T) {
	messages := []Message{{Role: "user", Content: "短"}}
	got := compactMessages(messages, 10000)
	if got[0].Content != "短" {
		t.Fatal("content should not change under budget")
	}
	if compactMessages(messages, 0)[0].Content != "短" {
		t.Fatal("zero budget disables compaction")
	}
}

// toolFailModel 让同一个坏工具连续失败，用于验证熔断。
type toolFailModel struct {
	mu    sync.Mutex
	calls int
	defs  [][]string
}

func (m *toolFailModel) Complete(_ context.Context, _ []Message, defs []ToolDef, onDelta func(string)) (Message, Usage, error) {
	m.mu.Lock()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	m.defs = append(m.defs, names)
	step := m.calls
	m.calls++
	m.mu.Unlock()

	if step < 2 {
		return Message{Role: "assistant", ToolCalls: []ToolCall{
			{ID: fmt.Sprintf("call-%d", step), Name: "boom", Arguments: "{}"},
		}}, Usage{}, nil
	}
	if onDelta != nil {
		onDelta("结论")
	}
	return Message{Role: "assistant", Content: "结论"}, Usage{}, nil
}

func TestRunDisablesToolAfterRepeatedFailures(t *testing.T) {
	registry := NewRegistry()
	registry.Add(funcTool{
		def: toolDef("boom"),
		run: func(context.Context, json.RawMessage) (any, error) {
			return nil, errors.New("tool exploded")
		},
	})
	model := &toolFailModel{}
	runtime := &Runtime{Model: model, Tools: registry, MaxSteps: 4}
	result, err := runtime.Run(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("expected partial success, got %v", err)
	}
	if result.Answer != "结论" {
		t.Fatalf("unexpected answer: %q", result.Answer)
	}
	if len(model.defs) < 3 {
		t.Fatalf("expected at least 3 model calls, got %d", len(model.defs))
	}
	for _, name := range model.defs[2] {
		if name == "boom" {
			t.Fatal("failing tool should be disabled after 2 failures")
		}
	}
}
