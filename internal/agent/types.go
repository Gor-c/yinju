// Package agent implements a small, read-only tool-calling loop modeled after
// the event-driven runtimes used by Pi and the OpenAI Agents SDK. It is
// intentionally independent from HTTP, databases and vector stores: callers
// register tools and provide a model client.
package agent

import (
	"context"
	"encoding/json"
)

// Message is one entry in the model transcript. Tool messages reference the
// assistant tool call they answer.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
	// FinishReason carries the provider stop reason (for example "length") so
	// the loop can detect a truncated assistant message.
	FinishReason string
}

// ToolCall is a model-requested tool invocation.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// ToolDef is the JSON-schema description advertised to the model.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Tool is a read-only capability the model may call.
type Tool interface {
	Definition() ToolDef
	Execute(ctx context.Context, args json.RawMessage) (any, error)
}

// Registry holds the tools available for one run.
type Registry struct {
	order []string
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

func (r *Registry) Add(tool Tool) {
	if tool == nil {
		return
	}
	name := tool.Definition().Name
	if _, exists := r.tools[name]; !exists {
		r.order = append(r.order, name)
	}
	r.tools[name] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) Definitions() []ToolDef {
	defs := make([]ToolDef, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

func (r *Registry) Names() []string {
	return append([]string(nil), r.order...)
}

// Usage is token accounting reported by the provider.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u *Usage) Add(other Usage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
}

// Model performs one model call. Streaming deltas are forwarded to onDelta;
// the complete assistant message (text plus tool calls) is returned.
type Model interface {
	Complete(ctx context.Context, messages []Message, tools []ToolDef, onDelta func(string)) (Message, Usage, error)
}

// Event is emitted while a run executes. The SSE handler serializes these
// directly, so field names are part of the HTTP contract.
type Event struct {
	Type      string `json:"type"`
	RunID     string `json:"run_id,omitempty"`
	Step      int    `json:"step,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
	Delta     string `json:"delta,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}
