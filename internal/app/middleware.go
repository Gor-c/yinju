package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

const requestIDKey = "request_id"

// ginLogWriter routes gin's own diagnostics (route registration and internal
// errors) through slog so nothing bypasses the configured log files.
type ginLogWriter struct {
	logger  *slog.Logger
	level   slog.Level
	message string
}

func (w ginLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\n")
	if text != "" {
		w.logger.Log(context.Background(), w.level, w.message, "text", text)
	}
	return len(p), nil
}

// requestIDMiddleware accepts an upstream X-Request-ID or creates one, then
// exposes it to handlers and the response.
func (s *Server) requestIDMiddleware(c *gin.Context) {
	id := strings.TrimSpace(c.GetHeader("X-Request-ID"))
	if id == "" || len(id) > 64 {
		id = newRequestID()
	}
	c.Set(requestIDKey, id)
	c.Header("X-Request-ID", id)
	c.Next()
}

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// accessLogMiddleware writes one structured record per request. Query strings
// are deliberately omitted so tokens are never written to logs.
func (s *Server) accessLogMiddleware(c *gin.Context) {
	started := time.Now()
	c.Next()
	if strings.HasPrefix(c.Request.URL.Path, "/assets/") {
		return
	}

	status := c.Writer.Status()
	attrs := []any{
		"request_id", c.GetString(requestIDKey),
		"method", c.Request.Method,
		"path", c.Request.URL.Path,
		"status", status,
		"latency_ms", time.Since(started).Milliseconds(),
		"ip", c.ClientIP(),
	}
	if c.Request.ContentLength > 0 {
		attrs = append(attrs, "request_bytes", c.Request.ContentLength)
	}
	if size := c.Writer.Size(); size > 0 {
		attrs = append(attrs, "response_bytes", size)
	}
	if len(c.Errors) > 0 {
		attrs = append(attrs, "gin_errors", c.Errors.String())
	}
	if status >= http.StatusInternalServerError {
		logging.HTTP().Error("http_request", attrs...)
		return
	}
	// Health probes arrive frequently; keep them out of the INFO access log.
	if c.Request.URL.Path == "/health" {
		logging.HTTP().Debug("http_request", attrs...)
		return
	}
	logging.HTTP().Info("http_request", attrs...)
}

func (s *Server) recoveryMiddleware(c *gin.Context) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		logging.App().Error("panic_recovered",
			"request_id", c.GetString(requestIDKey),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"panic", fmt.Sprint(recovered),
			"stack", string(debug.Stack()),
		)
		if c.Writer.Written() {
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}()
	c.Next()
}

// authMiddleware 在配置 API_TOKEN 时校验 /api/ 请求；未配置时完全放行，
// 适配本地开发。MCP 与静态资源有自己的入口，不走这里。
func (s *Server) authMiddleware() gin.HandlerFunc {
	token := strings.TrimSpace(os.Getenv("API_TOKEN"))
	if token == "" {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if provided == "" {
			provided = strings.TrimSpace(c.GetHeader("X-API-Token"))
		}
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
