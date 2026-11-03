package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// runEvent 是一条可回放的事件：seq 单调递增，用于断线后按位点续传。
type runEvent struct {
	Seq  int
	Name string
	Data []byte
	At   time.Time
}

// runStream 缓存一次 run 的 SSE 事件。客户端断线后可用 /agent/runs/:id/stream?after=N
// 从上次收到的位点继续，不必重新提问。
type runStream struct {
	mu     sync.Mutex
	events []runEvent
	done   bool
	wake   chan struct{}
	at     time.Time
}

func newRunStream() *runStream {
	return &runStream{wake: make(chan struct{}), at: time.Now()}
}

func (s *runStream) append(name string, data []byte) int {
	s.mu.Lock()
	seq := len(s.events) + 1
	s.events = append(s.events, runEvent{Seq: seq, Name: name, Data: data, At: time.Now()})
	s.at = time.Now()
	// 只保留最近 500 条，避免长 run 占用过多内存。
	if len(s.events) > 500 {
		s.events = s.events[len(s.events)-500:]
	}
	s.broadcast()
	s.mu.Unlock()
	return seq
}

func (s *runStream) finish() {
	s.mu.Lock()
	s.done = true
	s.broadcast()
	s.mu.Unlock()
}

// broadcast 唤醒所有等待者（close 后重建 channel）。
func (s *runStream) broadcast() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// since 返回位点之后的事件以及是否已结束。
func (s *runStream) since(after int) ([]runEvent, bool, chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runEvent, 0, len(s.events))
	for _, event := range s.events {
		if event.Seq > after {
			out = append(out, event)
		}
	}
	return out, s.done, s.wake
}

// runStreamHub 保存活跃与刚结束的 run，按 TTL 与数量回收。
type runStreamHub struct {
	mu   sync.Mutex
	runs map[string]*runStream
}

func newRunStreamHub() *runStreamHub {
	return &runStreamHub{runs: map[string]*runStream{}}
}

const (
	runStreamTTL  = 10 * time.Minute
	maxRunStreams = 100
)

func (h *runStreamHub) create(runID string) *runStream {
	// 测试或未初始化时降级为不缓存的流，不影响正常输出。
	if h == nil {
		return newRunStream()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// 顺带清理过期条目：完成超过 TTL 的 run 不再可续传。
	now := time.Now()
	for id, stream := range h.runs {
		stream.mu.Lock()
		expired := stream.done && now.Sub(stream.at) > runStreamTTL
		stream.mu.Unlock()
		if expired {
			delete(h.runs, id)
		}
	}
	if len(h.runs) >= maxRunStreams {
		for id := range h.runs {
			delete(h.runs, id)
			if len(h.runs) < maxRunStreams {
				break
			}
		}
	}
	stream := newRunStream()
	h.runs[runID] = stream
	return stream
}

func (h *runStreamHub) get(runID string) *runStream {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runs[runID]
}

func (h *runStreamHub) finish(runID string) {
	if h == nil {
		return
	}
	if stream := h.get(runID); stream != nil {
		stream.finish()
	}
}

// resumeAgentStream 从指定位点回放事件，并在 run 未结束时继续等待新事件。
func (s *Server) resumeAgentStream(c *gin.Context) {
	runID := c.Param("id")
	if runID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "run id is required"})
		return
	}
	stream := s.streams.get(runID)
	if stream == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "该运行的流式记录已过期，请重新提问"})
		return
	}
	after, _ := strconv.Atoi(c.Query("after"))

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	_, cancel := context.WithTimeout(c.Request.Context(), agentRunTimeout)
	defer cancel()

	for {
		events, done, wake := stream.since(after)
		for _, event := range events {
			writeSSE(c.Writer, event)
			after = event.Seq
		}
		flusher.Flush()
		if done {
			return
		}

		select {
		case <-wake:
			// 有新事件，继续回放。
		case <-time.After(15 * time.Second):
			_, _ = c.Writer.WriteString(": keep-alive\n\n")
			flusher.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}

// writeSSE 输出标准 SSE 帧，id 用于客户端记录位点。
func writeSSE(w io.Writer, event runEvent) {
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Name, event.Data)
}
