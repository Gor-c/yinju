package main

import (
	"log"
	"os"

	"knowledge-base/internal/app"
	"knowledge-base/internal/logging"
)

func main() {
	// 先加载 .env，再初始化日志，保证 LOG_* 配置也能从 .env 读取。
	// 已存在的环境变量优先，部署时由 systemd/容器注入的配置不会被覆盖。
	envPath := envOr("ENV_FILE", ".env")
	loadedEnvVars, envFileFound, envErr := loadEnvFile(envPath)

	closeLogs, err := logging.InitFromEnv()
	if err != nil {
		// Keep the process usable even when the log directory is unavailable.
		log.Printf("logging setup failed, falling back to stderr: %v", err)
		closeLogs = func() error { return nil }
	}

	logger := logging.App()
	if envErr != nil {
		logger.Warn("env_file_failed", "path", envPath, "error", envErr)
	} else if envFileFound {
		logger.Info("env_file_loaded", "path", envPath, "vars", loadedEnvVars)
	} else {
		logger.Debug("env_file_missing", "path", envPath)
	}
	server, err := app.New()
	if err != nil {
		logger.Error("startup_failed", "error", err)
		_ = closeLogs()
		os.Exit(1)
	}

	port := envOr("PORT", "8080")
	addr := envOr("LISTEN_ADDR", "127.0.0.1:"+port)
	logger.Info("server_starting",
		"addr", addr,
		"database_configured", os.Getenv("PG_DSN") != "",
		"vector_store", envOr("VECTOR_STORE", "pgvector"),
	)
	if err := server.Run(addr); err != nil {
		logger.Error("server_stopped", "error", err)
		_ = closeLogs()
		os.Exit(1)
	}
	_ = closeLogs()
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
