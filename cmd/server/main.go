package main

import (
	"log"
	"os"

	"knowledge-base/internal/app"
	"knowledge-base/internal/logging"
)

func main() {
	// 配置加载顺序决定优先级。loadEnvFile 对已存在的变量会跳过，
	// 所以「先加载的赢」：
	//   1. 进程环境变量（systemd / 容器注入，天然最高）
	//   2. data/instance.env —— 页面上保存的实例配置
	//   3. .env —— 人工维护的部署配置
	//   4. 代码默认值
	// 页面配置放在 .env 之前，是因为用户点了保存就该以它为准；
	// 而 .env 保留为兜底，删掉 instance.env 即可回到人工配置。
	instancePath := envOr("INSTANCE_ENV_FILE", "data/instance.env")
	if loaded, found, err := loadEnvFile(instancePath); err != nil {
		// 这里先不进日志系统，用 stderr 说明；接着仍继续启动，
		// 让 .env 接管，避免一个坏文件让服务完全起不来。
		log.Printf("instance config failed, falling back to .env: path=%s err=%v", instancePath, err)
	} else if found && loaded > 0 {
		log.Printf("instance config loaded: path=%s vars=%d", instancePath, loaded)
	}

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
