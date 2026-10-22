package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// 上游模型服务的错误分类与重试策略：限流（429）、超时、5xx 与网络抖动值得重试，
// 4xx 与上下文取消不应该重试。

const maxModelAttempts = 3

// 1s / 3s / 9s；测试中会调小以避免拖慢用例。
var modelRetryBase = time.Second

type upstreamError struct {
	Kind   string
	Status int
	Body   string
}

func (e *upstreamError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s API %d", e.Kind, e.Status)
	}
	return fmt.Sprintf("%s API %d: %s", e.Kind, e.Status, e.Body)
}

func retryableUpstream(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var upstream *upstreamError
	if errors.As(err, &upstream) {
		return upstream.Status == http.StatusTooManyRequests ||
			upstream.Status == http.StatusRequestTimeout ||
			upstream.Status >= 500
	}
	// 没有状态码：传输层错误。
	return true
}

func waitModelRetry(ctx context.Context, attempt int) bool {
	delay := modelRetryBase
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
