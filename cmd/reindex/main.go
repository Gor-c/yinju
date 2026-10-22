// Command reindex 在数据模型或向量结构变更后重建向量索引。
//
// 用法：
//
//	go run ./cmd/reindex            # 清空向量库并按 documents 重新排队
//	go run ./cmd/reindex -dry-run   # 只检查哪些文档可以重建
//
// 任务写入 index_jobs(queued)，由运行中的服务 worker 自动消费；
// 服务未启动时任务会保留，下次启动继续处理。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"knowledge-base/internal/vectorstore"
)

type docRow struct {
	DocID    string
	Filename string
	DocType  string
	FilePath string
	UnitID   string
}

func main() {
	envPath := flag.String("env", ".env", "path to the env file")
	dryRun := flag.Bool("dry-run", false, "only report what would be reindexed")
	flag.Parse()

	loadEnvFile(*envPath)
	dsn := strings.TrimSpace(os.Getenv("PG_DSN"))
	if dsn == "" {
		log.Fatal("PG_DSN is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	docs, err := loadDocuments(ctx, pool)
	if err != nil {
		log.Fatalf("load documents: %v", err)
	}
	if len(docs) == 0 {
		log.Println("没有需要重建的文档")
		return
	}

	fmt.Printf("共 %d 份文档：\n", len(docs))
	ready := make([]docRow, 0, len(docs))
	for _, doc := range docs {
		status := "可以重建"
		switch {
		case doc.UnitID == "":
			status = "跳过：未关联任何知识库"
		case doc.FilePath == "":
			status = "跳过：没有源文件路径"
		case !fileExists(doc.FilePath):
			status = "跳过：源文件不存在"
		default:
			ready = append(ready, doc)
		}
		fmt.Printf("  %s  %-40s  %s\n", doc.DocID, doc.Filename, status)
	}

	if len(ready) == 0 {
		log.Fatal("没有可重建的文档，未清空向量库")
	}
	if *dryRun {
		fmt.Printf("dry-run：将重建 %d 份文档\n", len(ready))
		return
	}

	store, err := vectorstore.New(os.Getenv("VECTOR_STORE"), pool)
	if err != nil {
		log.Fatalf("init vector store: %v", err)
	}
	if err := store.Reset(ctx); err != nil {
		log.Fatalf("reset vector store: %v", err)
	}
	fmt.Println("向量库已清空，正在重新排队…")

	queued := 0
	for i, doc := range ready {
		jobID := fmt.Sprintf("%d%03d", time.Now().UnixNano(), i%1000)
		_, err := pool.Exec(ctx, `INSERT INTO index_jobs
			(job_id,unit_id,doc_type,filename,file_path,status,progress,message,chunks,attempts,source_url,updated_at)
			VALUES($1,$2,$3,$4,$5,'queued',0,'重建索引',0,0,'',now())
			ON CONFLICT(job_id) DO NOTHING`,
			jobID, doc.UnitID, doc.DocType, doc.Filename, doc.FilePath)
		if err != nil {
			log.Printf("排队失败 %s: %v", doc.Filename, err)
			continue
		}
		queued++
	}
	fmt.Printf("已排队 %d 个索引任务；服务运行时会自动消费，否则下次启动继续。\n", queued)
}

func loadDocuments(ctx context.Context, pool *pgxpool.Pool) ([]docRow, error) {
	rows, err := pool.Query(ctx, `SELECT d.doc_id, d.filename, d.doc_type, d.file_path,
		COALESCE((SELECT du.unit_id FROM document_units du WHERE du.doc_id = d.doc_id
			ORDER BY du.added_at LIMIT 1), '')
		FROM documents d ORDER BY d.indexed_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []docRow{}
	for rows.Next() {
		var doc docRow
		if err := rows.Scan(&doc.DocID, &doc.Filename, &doc.DocType, &doc.FilePath, &doc.UnitID); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// loadEnvFile 读取简单的 KEY=VALUE 配置；已存在的环境变量优先。
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
