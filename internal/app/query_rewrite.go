package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"knowledge-base/internal/logging"
)

// rewriteSystemPrompt 是检索查询优化器的指令。它只做「把问题变成检索式」这一件事：
// 去掉疑问与指代、补齐行业同义表述，输出短查询而不是回答。
const rewriteSystemPrompt = `你是检索查询优化器。把用户问题改写成 1-3 条适合文档检索的查询。

规则：
1. 去掉疑问词、客套话与指代（如「这个」「上述」），保留实体、动作、限定条件；
2. 补齐行业同义表述：用户说「国产化」，同时给出「信创」「安全可靠改造」这类文档里的正式说法；
3. 每条不超过 20 字，只放关键词，用空格分隔，不要写成句子；
4. 不要回答问题，不要解释，只输出 JSON：{"queries":["查询1","查询2"]}`

// rewriteTimeout 控制改写耗时：超时就退回原问题，不让检索被小模型拖住。
const rewriteTimeout = 4 * time.Second

type rewritePayload struct {
	Queries []string `json:"queries"`
}

// rewriteQueries 用对话模型把问题改写成多个检索式。
// 任何一步失败都返回 nil，调用方按原问题检索——改写是增益，不是依赖。
func (s *Server) rewriteQueries(ctx context.Context, question string) []string {
	if strings.EqualFold(strings.TrimSpace(env("QUERY_REWRITE_ENABLED", "true")), "false") {
		return nil
	}
	if utf8.RuneCountInString(strings.TrimSpace(question)) < 4 {
		return nil
	}
	cfg := s.modelConfigFor(ctx)
	base := strings.TrimRight(cfg.LLMBaseURL, "/")
	if base == "" || missingModelCredential(base, cfg.LLMAPIKey) {
		return nil
	}

	body, _ := json.Marshal(map[string]any{
		"model": cfg.LLMModel,
		"messages": []map[string]string{
			{"role": "system", "content": rewriteSystemPrompt},
			{"role": "user", "content": question},
		},
		"temperature": 0,
		"max_tokens":  120,
	})
	reqCtx, cancel := context.WithTimeout(ctx, rewriteTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+cfg.LLMAPIKey)
	req.Header.Set("Content-Type", "application/json")

	started := time.Now()
	res, err := s.http.Do(req)
	if err != nil {
		logging.App().Warn("query_rewrite_failed", "error", err)
		return nil
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		logging.App().Warn("query_rewrite_status", "status", res.StatusCode, "body", truncateRunes(string(raw), 160))
		return nil
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return nil
	}
	queries := parseRewriteOutput(parsed.Choices[0].Message.Content, question)

	logging.App().Info("query_rewrite_completed",
		"question", truncateRunes(question, 40),
		"queries", strings.Join(queries, " | "),
		"duration_ms", time.Since(started).Milliseconds())
	return queries
}

// parseRewriteOutput 容忍三种输出形态：纯 JSON、带 ```json 围栏、逐行列表。
func parseRewriteOutput(content, question string) []string {
	text := strings.TrimSpace(content)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimPrefix(text, "```")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var payload rewritePayload
	candidates := []string{}
	if err := json.Unmarshal([]byte(text), &payload); err == nil {
		candidates = payload.Queries
	} else {
		// 兜底：模型没按 JSON 输出时，按行取非空内容。
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(strings.Trim(line, "-*0123456789. "))
			if line != "" {
				candidates = append(candidates, line)
			}
		}
	}

	max := envIntValue("QUERY_REWRITE_MAX", 3)
	seen := map[string]struct{}{strings.TrimSpace(question): {}}
	out := make([]string, 0, max)
	for _, item := range candidates {
		q := strings.TrimSpace(item)
		q = strings.Trim(q, "「」\"'")
		if utf8.RuneCountInString(q) < 2 || utf8.RuneCountInString(q) > 60 {
			continue
		}
		if _, dup := seen[q]; dup {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
		if len(out) >= max {
			break
		}
	}
	return out
}
