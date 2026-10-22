package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCompleteRetriesRateLimitThenSucceeds(t *testing.T) {
	previous := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = previous }()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":"rate limited"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL, "key", "model", srv.Client())
	message, _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if message.Content != "ok" {
		t.Fatalf("content = %q", message.Content)
	}
	if calls != 2 {
		t.Fatalf("expected one retry, calls = %d", calls)
	}
}

func TestCompleteDoesNotRetryAfterStreamingStarted(t *testing.T) {
	previous := retryBaseDelay
	retryBaseDelay = time.Millisecond
	defer func() { retryBaseDelay = previous }()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		// 先推一段增量，再断开，模拟流中途失败。
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n")
		w.(http.Flusher).Flush()
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	defer srv.Close()

	client := NewOpenAIClient(srv.URL, "key", "model", srv.Client())
	var got string
	_, _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, func(delta string) {
		got += delta
	})
	if err == nil {
		t.Fatal("expected an error after the stream broke")
	}
	if calls != 1 {
		t.Fatalf("must not retry after emitting deltas, calls = %d", calls)
	}
	if got != "part" {
		t.Fatalf("unexpected delta output: %q", got)
	}
}

func TestRetryableError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&apiError{Status: http.StatusTooManyRequests}, true},
		{&apiError{Status: http.StatusInternalServerError}, true},
		{&apiError{Status: http.StatusBadRequest}, false},
		{&apiError{Status: http.StatusUnauthorized}, false},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{errors.New("connection reset"), true},
	}
	for _, c := range cases {
		if got := retryableError(c.err); got != c.want {
			t.Errorf("retryableError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
