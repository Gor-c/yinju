// Package logging configures structured application, access and error logs.
//
// Layout (under LOG_DIR):
//
//	app.log     business, job and model logs (INFO and above)
//	access.log  HTTP request logs (INFO and above)
//	error.log   ERROR records from both loggers
//
// Rotated files use the pattern <name>-YYYYMMDD-HHMMSS.mmm.log.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

const (
	logFileNameApp    = "app"
	logFileNameAccess = "access"
	logFileNameError  = "error"
)

// Config controls log destinations and formatting.
type Config struct {
	Dir        string
	Format     string // text or json
	Level      string // debug, info, warn or error
	Console    bool   // mirror records to stdout
	MaxSizeMB  int    // rotate when a file exceeds this size, 0 disables
	MaxBackups int    // rotated files to keep per stream
}

var (
	appLogger  = slog.Default().With("component", "app")
	httpLogger = slog.Default().With("component", "http")
	closer     func() error
)

// App returns the logger for business, job and model events.
func App() *slog.Logger { return appLogger }

// HTTP returns the logger for request access records.
func HTTP() *slog.Logger { return httpLogger }

// InitFromEnv builds the logging configuration from LOG_* variables.
func InitFromEnv() (func() error, error) {
	return Init(Config{
		Dir:        env("LOG_DIR", "./logs"),
		Format:     env("LOG_FORMAT", "text"),
		Level:      env("LOG_LEVEL", "info"),
		Console:    envBool("LOG_CONSOLE", true),
		MaxSizeMB:  envInt("LOG_MAX_SIZE_MB", 50),
		MaxBackups: envInt("LOG_MAX_BACKUPS", 10),
	})
}

// Init opens log files and installs the package loggers. The returned function
// flushes and closes the files.
func Init(cfg Config) (func() error, error) {
	if cfg.Dir == "" {
		cfg.Dir = "./logs"
	}
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Dir, 0750); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", cfg.Dir, err)
	}

	appWriter, err := newRotateWriter(cfg.Dir, logFileNameApp, cfg.MaxSizeMB, cfg.MaxBackups)
	if err != nil {
		return nil, err
	}
	accessWriter, err := newRotateWriter(cfg.Dir, logFileNameAccess, cfg.MaxSizeMB, cfg.MaxBackups)
	if err != nil {
		appWriter.Close()
		return nil, err
	}
	errorWriter, err := newRotateWriter(cfg.Dir, logFileNameError, cfg.MaxSizeMB, cfg.MaxBackups)
	if err != nil {
		appWriter.Close()
		accessWriter.Close()
		return nil, err
	}

	appHandlers := []slog.Handler{
		newHandler(appWriter, cfg.Format, level),
		newHandler(errorWriter, cfg.Format, slog.LevelError),
	}
	httpHandlers := []slog.Handler{
		newHandler(accessWriter, cfg.Format, level),
		newHandler(errorWriter, cfg.Format, slog.LevelError),
	}
	if cfg.Console {
		console := newHandler(os.Stdout, cfg.Format, level)
		appHandlers = append(appHandlers, console)
		httpHandlers = append(httpHandlers, console)
	}

	appLogger = slog.New(multiHandler{handlers: appHandlers}).With("component", "app")
	httpLogger = slog.New(multiHandler{handlers: httpHandlers}).With("component", "http")
	slog.SetDefault(appLogger)
	if previous := closer; previous != nil {
		_ = previous()
	}
	closer = func() error {
		var first error
		for _, w := range []*rotateWriter{appWriter, accessWriter, errorWriter} {
			if err := w.Close(); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	return closer, nil
}

func newHandler(w io.Writer, format string, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(format, "json") {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid LOG_LEVEL %q (want debug, info, warn or error)", value)
	}
}

// multiHandler fans one record out to every enabled destination. Error records
// are cloned because each handler may retain the record.
type multiHandler struct{ handlers []slog.Handler }

func (m multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m multiHandler) Handle(ctx context.Context, record slog.Record) error {
	var first error
	for _, h := range m.handlers {
		if !h.Enabled(ctx, record.Level) {
			continue
		}
		if err := h.Handle(ctx, record.Clone()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return multiHandler{handlers: next}
}

func (m multiHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		next[i] = h.WithGroup(name)
	}
	return multiHandler{handlers: next}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(key))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}
