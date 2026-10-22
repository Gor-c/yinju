package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedModel struct {
	mu        sync.Mutex
	responses []Message
	errs      []error
	calls     int
	seen      [][]Message
	usage     Usage
}

func (m *scriptedModel) Complete(_ context.Context, messages []Message, _ []ToolDef, onDelta func(string)) (Message, Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen = append(m.seen, append([]Message(nil), messages...))
	index := m.calls
	m.calls++
	if index < len(m.errs) && m.errs[index] != nil {
		return Message{}, m.usage, m.errs[index]
	}
	if index >= len(m.responses) {
		return Message{Role: "assistant", Content: "done"}, m.usage, nil
	}
	response := m.responses[index]
	if onDelta != nil && response.Content != "" {
		onDelta(response.Content)
	}
	return response, m.usage, nil
}

type funcTool struct {
	def ToolDef
	run func(ctx context.Context, args json.RawMessage) (any, error)
}

func (t funcTool) Definition() ToolDef { return t.def }
func (t funcTool) Execute(ctx context.Context, args json.RawMessage) (any, error) {
	return t.run(ctx, args)
}

func toolDef(name string) ToolDef {
	return ToolDef{Name: name, Description: name, Parameters: map[string]any{"type": "object"}}
}

func TestRunExecutesToolThenAnswers(t *testing.T) {
	executed := 0
	registry := NewRegistry()
	registry.Add(funcTool{
		def: toolDef("profile"),
		run: func(_ context.Context, _ json.RawMessage) (any, error) {
			executed++
			return map[string]any{"name": "示例项目"}, nil
		},
	})

	model := &scriptedModel{
		responses: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "profile", Arguments: `{}`}}},
			{Role: "assistant", Content: "最终答案"},
		},
	}
	var events []Event
	runtime := &Runtime{Model: model, Tools: registry, MaxSteps: 4, Emit: func(event Event) {
		events = append(events, event)
	}}

	result, err := runtime.Run(context.Background(), []Message{{Role: "user", Content: "问"}})
	if err != nil {
		t.Fatal(err)
	}
	if executed != 1 {
		t.Fatalf("tool executions = %d, want 1", executed)
	}
	if result.Answer != "最终答案" || result.Steps != 2 || result.ToolCalls != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(model.seen) != 2 {
		t.Fatalf("model calls = %d, want 2", len(model.seen))
	}
	second := model.seen[1]
	last := second[len(second)-1]
	if last.Role != "tool" || last.ToolCallID != "call_1" || !strings.Contains(last.Content, "示例项目") {
		t.Fatalf("tool result was not appended: %#v", last)
	}

	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Type] = true
	}
	for _, want := range []string{"step_started", "tool_call", "tool_result", "message_delta", "step_completed"} {
		if !seen[want] {
			t.Fatalf("missing event %q in %#v", want, events)
		}
	}
}

func TestRunParallelToolsPreserveCallOrder(t *testing.T) {
	registry := NewRegistry()
	registry.Add(funcTool{
		def: toolDef("slow"),
		run: func(ctx context.Context, _ json.RawMessage) (any, error) {
			select {
			case <-time.After(40 * time.Millisecond):
			case <-ctx.Done():
			}
			return "slow-result", nil
		},
	})
	registry.Add(funcTool{
		def: toolDef("fast"),
		run: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "fast-result", nil
		},
	})

	model := &scriptedModel{responses: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_slow", Name: "slow", Arguments: `{}`},
			{ID: "call_fast", Name: "fast", Arguments: `{}`},
		}},
		{Role: "assistant", Content: "ok"},
	}}
	var toolResults []string
	var toolArguments []string
	runtime := &Runtime{Model: model, Tools: registry, Emit: func(event Event) {
		if event.Type == "tool_result" {
			toolResults = append(toolResults, event.Tool)
			toolArguments = append(toolArguments, event.Arguments)
		}
	}}

	if _, err := runtime.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(toolResults) != 2 || toolResults[0] != "slow" || toolResults[1] != "fast" {
		t.Fatalf("tool result order = %#v", toolResults)
	}
	if len(toolArguments) != 2 || toolArguments[0] != `{}` || toolArguments[1] != `{}` {
		t.Fatalf("tool result arguments = %#v", toolArguments)
	}

	messages := model.seen[1]
	tail := messages[len(messages)-2:]
	if tail[0].ToolCallID != "call_slow" || !strings.Contains(tail[0].Content, "slow-result") {
		t.Fatalf("first tool message = %#v", tail[0])
	}
	if tail[1].ToolCallID != "call_fast" || !strings.Contains(tail[1].Content, "fast-result") {
		t.Fatalf("second tool message = %#v", tail[1])
	}
}

func TestRunToolResultIncludesPerCallArguments(t *testing.T) {
	registry := NewRegistry()
	registry.Add(funcTool{
		def: toolDef("search"),
		run: func(_ context.Context, _ json.RawMessage) (any, error) {
			return "result", nil
		},
	})
	model := &scriptedModel{responses: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "search", Arguments: `{"query":"第一节点"}`},
			{ID: "call_2", Name: "search", Arguments: `{"query":"第二节点"}`},
		}},
		{Role: "assistant", Content: "ok"},
	}}
	var arguments []string
	runtime := &Runtime{Model: model, Tools: registry, Emit: func(event Event) {
		if event.Type == "tool_result" {
			arguments = append(arguments, event.Arguments)
		}
	}}

	if _, err := runtime.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 2 || arguments[0] != `{"query":"第一节点"}` || arguments[1] != `{"query":"第二节点"}` {
		t.Fatalf("tool result arguments = %#v", arguments)
	}
}

func TestRunInvalidArgumentsAreReturnedToModel(t *testing.T) {
	executed := false
	registry := NewRegistry()
	registry.Add(funcTool{
		def: toolDef("profile"),
		run: func(_ context.Context, _ json.RawMessage) (any, error) {
			executed = true
			return nil, nil
		},
	})
	model := &scriptedModel{responses: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "profile", Arguments: `not-json`}}},
		{Role: "assistant", Content: "ok"},
	}}
	runtime := &Runtime{Model: model, Tools: registry}
	if _, err := runtime.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("tool should not execute with invalid JSON arguments")
	}
	messages := model.seen[1]
	last := messages[len(messages)-1]
	if !strings.Contains(last.Content, "invalid arguments") {
		t.Fatalf("expected validation error in tool result, got %q", last.Content)
	}
}

func TestRunUnknownTool(t *testing.T) {
	model := &scriptedModel{responses: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "missing", Arguments: `{}`}}},
		{Role: "assistant", Content: "ok"},
	}}
	runtime := &Runtime{Model: model, Tools: NewRegistry()}
	var toolError string
	runtime.Emit = func(event Event) {
		if event.Type == "tool_result" {
			toolError = event.Error
		}
	}
	if _, err := runtime.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(toolError, "unknown tool") {
		t.Fatalf("tool error = %q", toolError)
	}
}

func TestRunStopsAtMaxSteps(t *testing.T) {
	registry := NewRegistry()
	registry.Add(funcTool{def: toolDef("loop"), run: func(context.Context, json.RawMessage) (any, error) { return "x", nil }})
	model := &scriptedModel{responses: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Name: "loop", Arguments: `{}`}}},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "2", Name: "loop", Arguments: `{}`}}},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "3", Name: "loop", Arguments: `{}`}}},
	}}
	runtime := &Runtime{Model: model, Tools: registry, MaxSteps: 2}
	result, err := runtime.Run(context.Background(), nil)
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v, want ErrMaxSteps", err)
	}
	if result.Steps != 2 {
		t.Fatalf("steps = %d, want 2", result.Steps)
	}
}

func TestRunToolPanicBecomesToolResult(t *testing.T) {
	registry := NewRegistry()
	registry.Add(funcTool{def: toolDef("boom"), run: func(context.Context, json.RawMessage) (any, error) {
		panic("boom")
	}})
	model := &scriptedModel{responses: []Message{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Name: "boom", Arguments: `{}`}}},
		{Role: "assistant", Content: "recovered"},
	}}
	runtime := &Runtime{Model: model, Tools: registry}
	if _, err := runtime.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	last := model.seen[1]
	if !strings.Contains(last[len(last)-1].Content, "panicked") {
		t.Fatalf("panic was not converted to a tool result: %#v", last[len(last)-1])
	}
}

func TestRunRejectsEmptyAnswer(t *testing.T) {
	model := &scriptedModel{responses: []Message{{Role: "assistant", Content: "   "}}}
	runtime := &Runtime{Model: model}
	if _, err := runtime.Run(context.Background(), nil); !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("err = %v, want ErrEmptyResponse", err)
	}
}

func TestRunRejectsTruncatedToolCalls(t *testing.T) {
	executed := 0
	registry := NewRegistry()
	registry.Add(funcTool{
		def: toolDef("profile"),
		run: func(_ context.Context, _ json.RawMessage) (any, error) {
			executed++
			return map[string]any{"name": "示例项目"}, nil
		},
	})

	// finish_reason=length means the model ran out of output budget while
	// streaming; salvaged arguments can look valid but be incomplete.
	model := &scriptedModel{
		responses: []Message{
			{Role: "assistant", FinishReason: "length", ToolCalls: []ToolCall{{ID: "call_1", Name: "profile", Arguments: `{"unit_`}}},
			{Role: "assistant", Content: "调用被截断，未执行"},
		},
	}
	runtime := &Runtime{Model: model, Tools: registry}
	result, err := runtime.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if executed != 0 {
		t.Fatalf("truncated tool call was executed %d times", executed)
	}
	if result.Answer != "调用被截断，未执行" {
		t.Fatalf("answer = %q", result.Answer)
	}
	// The model must be told why nothing happened, otherwise it retries blindly.
	found := false
	for _, msg := range model.seen[1] {
		if msg.Role == "tool" && strings.Contains(msg.Content, "truncated") {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool result does not explain the truncation: %#v", model.seen[1])
	}
}

func TestRunModelError(t *testing.T) {
	model := &scriptedModel{errs: []error{errors.New("provider down")}}
	runtime := &Runtime{Model: model}
	if _, err := runtime.Run(context.Background(), nil); err == nil {
		t.Fatal("expected model error")
	}
}

func TestMain(m *testing.M) {
	retryBaseDelay = time.Millisecond
	os.Exit(m.Run())
}
