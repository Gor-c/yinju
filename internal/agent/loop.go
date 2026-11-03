package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrMaxSteps      = errors.New("agent reached the maximum number of steps")
	ErrEmptyResponse = errors.New("model returned an empty response")
)

const (
	defaultMaxSteps           = 6
	defaultMaxToolResultBytes = 24000
	eventPreviewRunes         = 600
	// maxToolFailures：同一工具连续失败达到该次数后，本轮不再提供给它，
	// 避免模型反复重试同一个坏工具耗尽步数。
	maxToolFailures = 2
	// 压缩时工具结果保留的字符数。
	compactedToolRunes = 1200
)

// Result summarizes one complete run.
type Result struct {
	Answer    string
	Steps     int
	ToolCalls int
	Usage     Usage
}

// Runtime executes a tool-calling loop. Emit may be nil for non-streaming use.
type Runtime struct {
	Model              Model
	Tools              *Registry
	MaxSteps           int
	MaxToolResultBytes int
	// ContextBudget 是单次请求的上下文预算（估算 token）；超出后先压缩工具结果，
	// 再压缩较早的历史。0 表示不限制。
	ContextBudget int
	Emit          func(Event)
}

// Run appends the assistant/tool messages to the supplied transcript and
// returns the final answer. Tool calls within a step execute concurrently, but
// results are appended in the order the model requested them.
func (r *Runtime) Run(ctx context.Context, messages []Message) (Result, error) {
	if r.Model == nil {
		return Result{}, errors.New("agent model is not configured")
	}
	maxSteps := r.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}
	maxBytes := r.MaxToolResultBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxToolResultBytes
	}

	var emitMu sync.Mutex
	emit := func(event Event) {
		if r.Emit == nil {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		r.Emit(event)
	}

	var defs []ToolDef
	if r.Tools != nil {
		defs = r.Tools.Definitions()
	}

	// 工具熔断：连续失败多次的工具不再出现在下一步的工具定义里。
	failures := map[string]int{}
	disabled := map[string]bool{}

	result := Result{}
	for step := 1; step <= maxSteps; step++ {
		emit(Event{Type: "step_started", Step: step})

		// 超出预算时先压缩工具结果，再压缩历史，避免小窗口模型直接报错。
		messages = compactMessages(messages, r.ContextBudget)

		stepDefs := activeDefs(defs, disabled)
		// 最后一步只允许给结论：不再提供工具，并提示模型基于已有信息收尾，
		// 避免用户只看到「达到最大步数」而拿不到任何结果。
		if step == maxSteps && len(stepDefs) > 0 {
			stepDefs = nil
			messages = append(messages, Message{
				Role: "system",
				Content: "已达到本次工具调用上限。不要继续调用工具，请基于已经获得的信息直接给出结论；" +
					"如果信息不足以完整回答，先说明已确认的部分，再指出还缺什么，以及用户可以怎样追问。",
			})
		}

		// 累积本步已流出的文本：请求被取消或模型报错时，把已生成的部分作为
		// 答案返回，前端与历史都不会丢掉用户已经看到的内容。
		var streamed strings.Builder
		response, usage, err := r.Model.Complete(ctx, messages, stepDefs, func(delta string) {
			if delta != "" {
				streamed.WriteString(delta)
				emit(Event{Type: "message_delta", Step: step, Delta: delta})
			}
		})
		result.Usage.Add(usage)
		if err != nil {
			result.Steps = step
			if text := strings.TrimSpace(streamed.String()); text != "" {
				result.Answer = text
			}
			return result, err
		}
		if response.Role == "" {
			response.Role = "assistant"
		}
		for i := range response.ToolCalls {
			if response.ToolCalls[i].ID == "" {
				response.ToolCalls[i].ID = fmt.Sprintf("call_%d_%d", step, i)
			}
		}
		messages = append(messages, response)

		if len(response.ToolCalls) == 0 {
			emit(Event{Type: "step_completed", Step: step})
			if strings.TrimSpace(response.Content) == "" {
				result.Steps = step
				return result, ErrEmptyResponse
			}
			result.Answer = response.Content
			result.Steps = step
			return result, nil
		}

		// 最后一步模型仍然要求调工具：用已经流出的文字兜底，实在没有才报错。
		if step == maxSteps {
			emit(Event{Type: "step_completed", Step: step})
			result.Steps = step
			result.Answer = strings.TrimSpace(response.Content)
			if result.Answer != "" {
				return result, nil
			}
			return result, ErrMaxSteps
		}

		result.ToolCalls += len(response.ToolCalls)

		// When the model hits its output limit mid-message, streamed tool
		// arguments can still parse as valid JSON while being silently
		// incomplete. None of those calls is safe to execute.
		if response.FinishReason == "length" {
			for _, call := range response.ToolCalls {
				err := fmt.Errorf("tool call %s was truncated by the model output limit and was not executed", call.Name)
				content := errorContent(err)
				emit(Event{
					Type:      "tool_result",
					Step:      step,
					Tool:      call.Name,
					Arguments: truncateText(call.Arguments, eventPreviewRunes),
					Result:    truncateText(content, eventPreviewRunes),
					Error:     err.Error(),
				})
				messages = append(messages, Message{
					Role:       "tool",
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    content,
				})
			}
			emit(Event{Type: "step_completed", Step: step})
			continue
		}

		contents, errs := r.executeTools(ctx, step, response.ToolCalls, maxBytes, emit)
		for i, call := range response.ToolCalls {
			if errs[i] != nil {
				failures[call.Name]++
			} else {
				delete(failures, call.Name)
			}
			if failures[call.Name] >= maxToolFailures && !disabled[call.Name] {
				disabled[call.Name] = true
				messages = append(messages, Message{
					Role: "system",
					Content: fmt.Sprintf("工具 %s 连续失败 %d 次，本次对话不再提供它。请改用其他工具，或直接说明无法获取并给出可行建议。",
						call.Name, failures[call.Name]),
				})
			}
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    contents[i],
			})
		}
		emit(Event{Type: "step_completed", Step: step})
	}

	result.Steps = maxSteps
	return result, ErrMaxSteps
}

type preparedCall struct {
	call ToolCall
	tool Tool
	args json.RawMessage
	err  error
}

func (r *Runtime) executeTools(ctx context.Context, step int, calls []ToolCall, maxBytes int, emit func(Event)) ([]string, []error) {
	plans := make([]preparedCall, len(calls))
	for i, call := range calls {
		plan := preparedCall{call: call, args: json.RawMessage("{}")}
		if call.Arguments != "" {
			plan.args = json.RawMessage(call.Arguments)
		}
		emit(Event{
			Type:      "tool_call",
			Step:      step,
			Tool:      call.Name,
			Arguments: truncateText(call.Arguments, eventPreviewRunes),
		})
		tool, ok := Tool(nil), false
		if r.Tools != nil {
			tool, ok = r.Tools.Get(call.Name)
		}
		if !ok {
			plan.err = fmt.Errorf("unknown tool: %s", call.Name)
		} else if !json.Valid(plan.args) {
			plan.err = fmt.Errorf("invalid arguments for %s: arguments must be valid JSON", call.Name)
		} else {
			plan.tool = tool
		}
		plans[i] = plan
	}

	contents := make([]string, len(plans))
	errs := make([]error, len(plans))
	latencies := make([]int64, len(plans))

	var wg sync.WaitGroup
	for i := range plans {
		plan := plans[i]
		if plan.err != nil {
			errs[i] = plan.err
			contents[i] = errorContent(plan.err)
			continue
		}
		wg.Add(1)
		go func(index int, plan preparedCall) {
			defer wg.Done()
			started := time.Now()
			defer func() { latencies[index] = time.Since(started).Milliseconds() }()
			defer func() {
				if recovered := recover(); recovered != nil {
					errs[index] = fmt.Errorf("tool %s panicked: %v", plan.call.Name, recovered)
					contents[index] = errorContent(errs[index])
				}
			}()

			data, err := plan.tool.Execute(ctx, plan.args)
			if err != nil {
				errs[index] = err
				contents[index] = errorContent(err)
				return
			}
			contents[index] = encodeToolResult(data, maxBytes)
		}(i, plan)
	}
	wg.Wait()

	for i, plan := range plans {
		event := Event{
			Type:      "tool_result",
			Step:      step,
			Tool:      plan.call.Name,
			Arguments: truncateText(plan.call.Arguments, eventPreviewRunes),
			Result:    truncateText(contents[i], eventPreviewRunes),
			LatencyMS: latencies[i],
		}
		if errs[i] != nil {
			event.Error = errs[i].Error()
		}
		emit(event)
	}
	return contents, errs
}

func encodeToolResult(data any, limit int) string {
	encoded, err := json.Marshal(data)
	if err != nil {
		return errorContent(err)
	}
	if len(encoded) <= limit {
		return string(encoded)
	}
	wrapped, err := json.Marshal(map[string]any{
		"truncated": true,
		"preview":   string(encoded[:limit]),
	})
	if err != nil {
		return errorContent(err)
	}
	return string(wrapped)
}

func errorContent(err error) string {
	encoded, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(encoded)
}

func truncateText(value string, limit int) string {
	if limit <= 0 || value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// activeDefs 过滤掉被熔断的工具；全部被熔断时返回空（模型只能给结论）。
func activeDefs(defs []ToolDef, disabled map[string]bool) []ToolDef {
	if len(disabled) == 0 {
		return defs
	}
	out := make([]ToolDef, 0, len(defs))
	for _, def := range defs {
		if disabled[def.Name] {
			continue
		}
		out = append(out, def)
	}
	return out
}

// estimateTokens 粗略估算 token 数：中文按 1 字 ≈ 0.7 token，英文与符号偏保守。
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	runes := 0
	for _, r := range text {
		if r > 127 {
			runes += 2
		} else {
			runes++
		}
	}
	return runes / 2
}

// compactMessages 在超出上下文预算时压缩内容，不改动消息结构
// （保留 assistant.tool_calls 与 tool 消息的配对，避免请求被上游拒绝）：
// 先把较早的工具结果截断，再把较早的历史回复截断。
func compactMessages(messages []Message, budget int) []Message {
	if budget <= 0 || len(messages) == 0 {
		return messages
	}
	total := 0
	for _, m := range messages {
		total += estimateTokens(m.Content)
	}
	if total <= budget {
		return messages
	}

	out := make([]Message, len(messages))
	copy(out, messages)

	// 第一轮：压缩工具结果（从最早开始）。
	for i := range out {
		if out[i].Role != "tool" {
			continue
		}
		trimmed := truncateText(out[i].Content, compactedToolRunes)
		if trimmed == out[i].Content {
			continue
		}
		total -= estimateTokens(out[i].Content) - estimateTokens(trimmed)
		out[i].Content = trimmed
		if total <= budget {
			return out
		}
	}

	// 第二轮：压缩较早的历史回复，保留最近两条完整。
	keepFrom := len(out) - 3
	if keepFrom < 0 {
		keepFrom = 0
	}
	for i := range out {
		if i >= keepFrom {
			break
		}
		if out[i].Role != "assistant" && out[i].Role != "user" {
			continue
		}
		trimmed := truncateText(out[i].Content, 600)
		if trimmed == out[i].Content {
			continue
		}
		total -= estimateTokens(out[i].Content) - estimateTokens(trimmed)
		out[i].Content = trimmed
		if total <= budget {
			return out
		}
	}
	return out
}
