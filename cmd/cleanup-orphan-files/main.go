// Command cleanup-orphan-files 找出上传目录里没有被任何文档或任务引用的文件。
//
// 用法：
//
//	go run ./cmd/cleanup-orphan-files           # 只列出
//	go run ./cmd/cleanup-orphan-files -apply    # 删除
//
// 判定依据是 documents.file_path 与 index_jobs.file_path：两者都不引用的文件
// 才视为孤儿，避免误删仍可重试的失败任务原件。
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	envPath := flag.String("env", ".env", "path to the env file")
	apply := flag.Bool("apply", false, "delete orphan files")
	flag.Parse()

	loadEnvFile(*envPath)
	dsn := strings.TrimSpace(os.Getenv("PG_DSN"))
	if dsn == "" {
		log.Fatal("PG_DSN is required")
	}
	root := strings.TrimSpace(os.Getenv("UPLOAD_DIR"))
	if root == "" {
		root = "./data/uploads"
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		log.Fatalf("resolve upload dir: %v", err)
	}
	if info, err := os.Stat(absRoot); err != nil || !info.IsDir() {
		log.Fatalf("upload dir not found: %s", absRoot)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	referenced := map[string]bool{}
	collect := func(query string) {
		rows, err := pool.Query(ctx, query)
		if err != nil {
			log.Fatalf("load references: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				log.Fatalf("scan reference: %v", err)
			}
			if key := canonical(path, absRoot); key != "" {
				referenced[key] = true
			}
		}
		if err := rows.Err(); err != nil {
			log.Fatalf("iterate references: %v", err)
		}
	}
	collect(`SELECT file_path FROM documents WHERE file_path <> ''`)
	collect(`SELECT file_path FROM index_jobs WHERE file_path <> ''`)

	total, orphans := 0, []string{}
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		total++
		if !referenced[canonical(path, absRoot)] {
			orphans = append(orphans, path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("walk upload dir: %v", err)
	}

	fmt.Printf("上传目录：%s\n共 %d 个文件，其中 %d 个未被引用。\n", absRoot, total, len(orphans))
	for _, path := range orphans {
		fmt.Println("  ", strings.TrimPrefix(path, absRoot+string(filepath.Separator)))
	}
	if !*apply {
		if len(orphans) > 0 {
			fmt.Println("（只列出，不删除；加 -apply 执行删除）")
		}
		return
	}
	deleted := 0
	for _, path := range orphans {
		if err := os.Remove(path); err != nil {
			log.Printf("删除失败 %s: %v", path, err)
			continue
		}
		deleted++
	}
	fmt.Printf("已删除 %d 个孤儿文件。\n", deleted)
}

// canonical 把路径统一成相对上传目录的形式，兼容库里存的相对路径与绝对路径。
func canonical(path, absRoot string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if abs, err := filepath.Abs(clean); err == nil {
		if rel, err := filepath.Rel(absRoot, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if rel, err := filepath.Rel(absRoot, clean); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return ""
}

func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}
