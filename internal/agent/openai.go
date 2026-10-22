package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// OpenAIClient calls an OpenAI-compatible /chat/completions endpoint with
// streaming and tool support. It deliberately has no client-level timeout so
// long streams are governed by the request context.
type OpenAIClient struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func NewOpenAIClient(baseURL, apiKey, model string, client *http.Client) *OpenAIClient {
	if client == nil {
		client = &http.Client{}
	}
	return &OpenAIClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    client,
	}
}

const maxRetryAttempts = 3

// 1s / 3s / 9s，给上游限流留出恢复窗口；测试中可调小。
var retryBaseDelay = time.Second

func (c *OpenAIClient) Complete(ctx context.Context, messages []Message, tools []ToolDef, onDelta func(string)) (Message, Usage, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		// 一旦已经推过增量，就不能重试：否则前端会收到重复内容。
		emitted := false
		message, usage, err := c.do(ctx, messages, tools, true, func(delta string) {
			if delta != "" {
				emitted = true
			}
			if onDelta != nil {
				onDelta(delta)
			}
		})
		if err == nil {
			return message, usage, nil
		}
		lastErr = err

		// Some OpenAI-compatible providers reject streaming tool calls. Fall
		// back to a non-streaming request so the agent still works.
		var apiErr *apiError
		if len(tools) > 0 && errors.As(err, &apiErr) &&
			(apiErr.Status == http.StatusBadRequest || apiErr.Status == http.StatusUnprocessableEntity) {
			return c.do(ctx, messages, tools, false, onDelta)
		}
		if emitted || attempt == maxRetryAttempts || !retryableError(err) {
			break
		}
		if !waitBeforeRetry(ctx, attempt) {
			break
		}
	}
	return Message{}, Usage{}, lastErr
}

// retryableError 判断是否值得重试：限流、超时、5xx 与网络错误；
// 上下文取消和 4xx（参数问题）重试没有意义。
func retryableError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.Status == http.StatusTooManyRequests ||
			apiErr.Status == http.StatusRequestTimeout ||
			apiErr.Status >= 500
	}
	// 没有状态码说明是传输层错误（连接重置等），可以重试。
	return true
}

func waitBeforeRetry(ctx context.Context, attempt int) bool {
	delay := retryBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 3
	}
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *OpenAIClient) do(ctx context.Context, messages []Message, tools []ToolDef, stream bool, onDelta func(string)) (Message, Usage, error) {
	payload := map[string]any{
		"model":       c.Model,
		"messages":    toOpenAIMessages(messages),
		"temperature": 0.2,
		"stream":      stream,
	}
	if len(tools) > 0 {
		payload["tools"] = toOpenAITools(tools)
		payload["tool_choice"] = "auto"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Message{}, Usage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Message{}, Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream, application/json")

	response, err := c.HTTP.Do(req)
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Message{}, Usage{}, &apiError{Status: response.StatusCode, Body: strings.TrimSpace(string(raw))}
	}

	if stream && strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return parseStream(response.Body, onDelta)
	}
	return parseResponse(response.Body, onDelta)
}

type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("chat API %d", e.Status)
	}
	return fmt.Sprintf("chat API %d: %s", e.Status, e.Body)
}

func toOpenAIMessages(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "assistant":
			item := map[string]any{"role": "assistant"}
			if len(message.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, map[string]any{
						"id":   call.ID,
						"type": "function",
						"function": map[string]any{
							"name":      call.Name,
							"arguments": call.Arguments,
						},
					})
				}
				item["tool_calls"] = calls
				if message.Content != "" {
					item["content"] = message.Content
				} else {
					item["content"] = nil
				}
			} else {
				item["content"] = message.Content
			}
			out = append(out, item)
		case "tool":
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": message.ToolCallID,
				"content":      message.Content,
			})
		default:
			out = append(out, map[string]any{"role": message.Role, "content": message.Content})
		}
	}
	return out
}

func toOpenAITools(defs []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		params := def.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        def.Name,
				"description": def.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

type callAccumulator struct {
	id   string
	name string
	args strings.Builder
}

func parseStream(body io.Reader, onDelta func(string)) (Message, Usage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var content strings.Builder
	calls := map[int]*callAccumulator{}
	var usage Usage
	seen := false
	finishReason := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Message{}, usage, fmt.Errorf("decode chat stream: %w", err)
		}
		seen = true
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finishReason = *choice.FinishReason
			}
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				content.WriteString(*choice.Delta.Content)
				if onDelta != nil {
					onDelta(*choice.Delta.Content)
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				acc, ok := calls[call.Index]
				if !ok {
					acc = &callAccumulator{}
					calls[call.Index] = acc
				}
				if call.ID != "" {
					acc.id = call.ID
				}
				if call.Function.Name != "" {
					acc.name += call.Function.Name
				}
				acc.args.WriteString(call.Function.Arguments)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Message{}, usage, err
	}
	if !seen {
		return Message{}, usage, errors.New("chat API returned an empty stream")
	}

	message := Message{Role: "assistant", Content: content.String(), FinishReason: finishReason}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		acc := calls[index]
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: acc.args.String(),
		})
	}
	return message, usage, nil
}

type fullResponse struct {
	Choices []struct {
		Message struct {
			Content   *string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

func parseResponse(body io.Reader, onDelta func(string)) (Message, Usage, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 8*1024*1024))
	if err != nil {
		return Message{}, Usage{}, err
	}
	var parsed fullResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Message{}, Usage{}, fmt.Errorf("decode chat response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return Message{}, Usage{}, errors.New("chat API returned no choices")
	}
	choice := parsed.Choices[0].Message
	message := Message{Role: "assistant"}
	if choice.Content != nil {
		message.Content = *choice.Content
		if onDelta != nil && message.Content != "" {
			onDelta(message.Content)
		}
	}
	for _, call := range choice.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	usage := Usage{}
	if parsed.Usage != nil {
		usage = *parsed.Usage
	}
	return message, usage, nil
}
