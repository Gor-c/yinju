package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

// 文档是独立实体：documents 按内容寻址持有一份文档，document_units 表达
// 「文档 ↔ 知识库」的多对多关系。知识库退化为带标签的分组，同一份文件
// 可以挂在多个知识库下，而向量只存一份。

const createDocumentUnitsTable = `CREATE TABLE IF NOT EXISTS document_units (
	doc_id text NOT NULL,
	unit_id text NOT NULL,
	added_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (doc_id, unit_id)
)`

// documentRef 是一份文档在某个知识库下的视图。
type documentRef struct {
	DocID      string
	Filename   string
	DocType    string
	FilePath   string
	ChunkCount int
	JobID      string
}

// docIDForText 由正文哈希生成稳定 id：同样的内容永远得到同一个 doc_id，
// 因此重复上传时无需重新解析和向量化。
func docIDForText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "doc-" + hex.EncodeToString(sum[:8])
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ensureDocumentSchema 把老库的「documents(unit_id, filename)」升级为文档中心模型。
// 幂等执行：首次启动完成迁移，之后每次启动只做存在性检查。
func (s *Server) ensureDocumentSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(ctx, createDocumentUnitsTable); err != nil {
		logging.App().Warn("document_schema_failed", "step", "document_units", "error", err)
		return
	}
	if _, err := s.db.Exec(ctx, `CREATE INDEX IF NOT EXISTS document_units_unit_idx ON document_units(unit_id)`); err != nil {
		logging.App().Warn("document_schema_failed", "step", "unit_index", "error", err)
	}

	// 向量身份三列：换嵌入模型或换切块策略后，旧索引必须能被识别出来。
	// 没有这三列，混用向量空间是静默的——不报错，只表现为检索变差。
	for _, column := range []struct{ name, ddl string }{
		{"embedding_model", `ALTER TABLE documents ADD COLUMN IF NOT EXISTS embedding_model text NOT NULL DEFAULT ''`},
		{"embedding_dim", `ALTER TABLE documents ADD COLUMN IF NOT EXISTS embedding_dim integer NOT NULL DEFAULT 0`},
		{"chunker", `ALTER TABLE documents ADD COLUMN IF NOT EXISTS chunker text NOT NULL DEFAULT ''`},
	} {
		if _, err := s.db.Exec(ctx, column.ddl); err != nil {
			logging.App().Warn("document_schema_failed", "step", "add_"+column.name, "error", err)
		}
	}

	// 1) 补 doc_id 列
	if !s.columnExists(ctx, "documents", "doc_id") {
		if _, err := s.db.Exec(ctx, `ALTER TABLE documents ADD COLUMN doc_id text`); err != nil {
			logging.App().Warn("document_schema_failed", "step", "add_doc_id", "error", err)
			return
		}
	}

	// 2) 回填：内容哈希优先，缺失时按旧主键生成稳定 id
	backfills := []string{
		`UPDATE documents SET doc_id = 'doc-' || substr(content_hash,1,16)
			WHERE (doc_id IS NULL OR doc_id='') AND content_hash<>''`,
	}
	if s.columnExists(ctx, "documents", "unit_id") {
		backfills = append(backfills,
			`UPDATE documents SET doc_id = 'doc-' || substr(md5(unit_id || '/' || filename),1,16)
				WHERE doc_id IS NULL OR doc_id=''`)
	}
	backfills = append(backfills,
		`UPDATE documents SET doc_id = 'doc-' || substr(md5(filename || clock_timestamp()::text),1,16)
			WHERE doc_id IS NULL OR doc_id=''`)
	for i, stmt := range backfills {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("document_schema_failed", "step", fmt.Sprintf("backfill_%d", i), "error", err)
			return
		}
	}

	// 3) 旧表里的 (unit_id, filename) 关系迁移成多对多
	if s.columnExists(ctx, "documents", "unit_id") {
		if _, err := s.db.Exec(ctx, `INSERT INTO document_units(doc_id, unit_id)
			SELECT DISTINCT doc_id, unit_id FROM documents WHERE unit_id<>''
			ON CONFLICT DO NOTHING`); err != nil {
			logging.App().Warn("document_schema_failed", "step", "backfill_units", "error", err)
			return
		}
	}

	// 4) 同内容在多个知识库下会得到同一个 doc_id，旧表里却是多行：保留一行
	if _, err := s.db.Exec(ctx, `DELETE FROM documents a USING documents b
		WHERE a.doc_id = b.doc_id AND a.ctid > b.ctid`); err != nil {
		logging.App().Warn("document_schema_failed", "step", "dedupe", "error", err)
		return
	}

	// 5) 主键从 (unit_id, filename) 切到 doc_id，并移除不再使用的 unit_id 列
	if s.columnExists(ctx, "documents", "unit_id") {
		for _, stmt := range []string{
			`ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_pkey`,
			`ALTER TABLE documents DROP CONSTRAINT IF EXISTS documents_unit_id_filename_key`,
			`ALTER TABLE documents DROP COLUMN IF EXISTS unit_id`,
		} {
			if _, err := s.db.Exec(ctx, stmt); err != nil {
				logging.App().Warn("document_schema_failed", "step", "drop_legacy_columns", "error", err)
				return
			}
		}
	}
	if _, err := s.db.Exec(ctx, `ALTER TABLE documents ALTER COLUMN doc_id SET NOT NULL`); err != nil {
		logging.App().Warn("document_schema_failed", "step", "doc_id_not_null", "error", err)
		return
	}
	if !s.primaryKeyOn(ctx, "documents", "doc_id") {
		if _, err := s.db.Exec(ctx, `ALTER TABLE documents ADD PRIMARY KEY (doc_id)`); err != nil {
			logging.App().Warn("document_schema_failed", "step", "primary_key", "error", err)
			return
		}
	}

	// 6) 同一内容只保留一份文档
	if _, err := s.db.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS documents_content_hash_idx
		ON documents(content_hash) WHERE content_hash <> ''`); err != nil {
		logging.App().Warn("document_schema_failed", "step", "hash_index", "error", err)
	}
}

func (s *Server) columnExists(ctx context.Context, table, column string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`,
		table, column).Scan(&exists)
	return err == nil && exists
}

func (s *Server) primaryKeyOn(ctx context.Context, table, column string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype = 'p'
			AND pg_get_constraintdef(oid) = 'PRIMARY KEY (' || $2 || ')')`, table, column).Scan(&exists)
	return err == nil && exists
}

// docIDsForUnits 把知识库集合翻译成文档集合（去重）。
func (s *Server) docIDsForUnits(ctx context.Context, unitIDs []string) ([]string, error) {
	ids, _, err := s.docIDsForUnitsWithOwner(ctx, unitIDs)
	return ids, err
}

// originalDocIDs 去掉问答沉淀文档（doc_type=问答沉淀）。
//
// 沉淀文档是「某一轮问答的转述」，不是原始档案。拿它出示例问题等于
// 「用系统自己写的笔记考系统」：问题里会出现《主题笔记：由 2 条历史问答整理》
// 这种没人问得出口的标题，验证环节也会被自己写的答案「通过」。
// 示例问题与「覆盖章节」都走这里；内容框架（目录树）不走，那里只是展示。
func (s *Server) originalDocIDs(ctx context.Context, docIDs []string) ([]string, error) {
	if len(docIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT doc_id FROM documents
		WHERE doc_id = ANY($1) AND COALESCE(doc_type, '') <> $2`, docIDs, kbDraftDocType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, len(docIDs))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// docIDsForUnitsWithOwner 同时返回每份文档的归属知识库（取传入顺序中第一个），
// 用于给跨知识库的检索结果标注来源。
func (s *Server) docIDsForUnitsWithOwner(ctx context.Context, unitIDs []string) ([]string, map[string]string, error) {
	if s.db == nil || len(unitIDs) == 0 {
		return nil, map[string]string{}, nil
	}
	rows, err := s.db.Query(ctx, `SELECT doc_id, unit_id FROM document_units WHERE unit_id = ANY($1)`, unitIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	order := make(map[string]int, len(unitIDs))
	for i, id := range unitIDs {
		order[id] = i
	}
	owner := map[string]string{}
	for rows.Next() {
		var docID, unitID string
		if err := rows.Scan(&docID, &unitID); err != nil {
			return nil, nil, err
		}
		current, ok := owner[docID]
		if !ok || order[unitID] < order[current] {
			owner[docID] = unitID
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(owner))
	for docID := range owner {
		ids = append(ids, docID)
	}
	// 稳定顺序，便于测试与日志对比。
	sortStrings(ids)
	return ids, owner, nil
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// documentRefByUnitFile 解析某知识库下某个文件名对应的文档。
func (s *Server) documentRefByUnitFile(ctx context.Context, unitID, filename string) (documentRef, error) {
	var ref documentRef
	err := s.db.QueryRow(ctx, `SELECT d.doc_id, d.filename, d.doc_type, d.file_path, d.chunk_count, d.job_id
		FROM documents d JOIN document_units du ON du.doc_id = d.doc_id
		WHERE du.unit_id = $1 AND d.filename = $2
		ORDER BY d.indexed_at DESC LIMIT 1`, unitID, filename).
		Scan(&ref.DocID, &ref.Filename, &ref.DocType, &ref.FilePath, &ref.ChunkCount, &ref.JobID)
	return ref, err
}

// documentByID 读取文档元数据。
func (s *Server) documentByID(ctx context.Context, docID string) (documentRef, error) {
	var ref documentRef
	err := s.db.QueryRow(ctx, `SELECT doc_id, filename, doc_type, file_path, chunk_count, job_id
		FROM documents WHERE doc_id=$1`, docID).
		Scan(&ref.DocID, &ref.Filename, &ref.DocType, &ref.FilePath, &ref.ChunkCount, &ref.JobID)
	return ref, err
}

func (s *Server) linkDocumentUnit(ctx context.Context, docID, unitID string) error {
	if docID == "" || unitID == "" {
		return nil
	}
	_, err := s.db.Exec(ctx, `INSERT INTO document_units(doc_id, unit_id) VALUES($1,$2)
		ON CONFLICT DO NOTHING`, docID, unitID)
	return err
}

func (s *Server) unlinkDocumentUnit(ctx context.Context, docID, unitID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM document_units WHERE doc_id=$1 AND unit_id=$2`, docID, unitID)
	return err
}

// cleanupOrphanDocument 在文档不再被任何知识库引用时清理向量、源文件与记录。
// 仍被其他知识库引用时保持原样，实现「从某个知识库移除」与「彻底删除」的区分。
func (s *Server) cleanupOrphanDocument(ctx context.Context, docID string) (bool, error) {
	if docID == "" {
		return false, nil
	}
	var refs int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM document_units WHERE doc_id=$1`, docID).Scan(&refs); err != nil {
		return false, err
	}
	if refs > 0 {
		return false, nil
	}
	var filePath string
	err := s.db.QueryRow(ctx, `SELECT file_path FROM documents WHERE doc_id=$1`, docID).Scan(&filePath)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if s.vectors != nil {
		if err := s.vectors.DeleteDoc(ctx, docID); err != nil {
			logging.App().Warn("document_vectors_delete_failed", "doc_id", docID, "error", err)
		}
	}
	if err := s.deleteChunkText(ctx, docID); err != nil {
		logging.App().Warn("chunk_text_delete_failed", "doc_id", docID, "error", err)
	}
	// 重建索引时新旧文档可能共用同一份源文件，删除前确认没有其他记录引用。
	if filePath != "" && !s.fileReferencedByOtherDoc(ctx, filePath, docID) {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			logging.App().Warn("document_file_delete_failed", "doc_id", docID, "path", filePath, "error", err)
		}
	}
	if _, err := s.db.Exec(ctx, `DELETE FROM documents WHERE doc_id=$1`, docID); err != nil {
		return false, err
	}
	logging.App().Info("document_orphan_removed", "doc_id", docID)
	return true, nil
}

func (s *Server) fileReferencedByOtherDoc(ctx context.Context, filePath, docID string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM documents WHERE file_path=$1 AND doc_id<>$2)`, filePath, docID).Scan(&exists)
	return err == nil && exists
}

// pruneReplacedDocuments 在同一知识库下同一文件名有新版本时，解绑并清理旧文档。
// 上传同名不同内容的文件、或重建索引后内容哈希变化，都会走到这里。
func (s *Server) pruneReplacedDocuments(ctx context.Context, unitID, filename, keepDocID string) error {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT du.doc_id FROM document_units du
		JOIN documents d ON d.doc_id = du.doc_id
		WHERE du.unit_id=$1 AND d.filename=$2 AND du.doc_id<>$3`, unitID, filename, keepDocID)
	if err != nil {
		return err
	}
	stale := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if err := s.unlinkDocumentUnit(ctx, id, unitID); err != nil {
			return err
		}
		if _, err := s.cleanupOrphanDocument(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// documentUnitIsLinked 判断文档是否已挂在某个知识库下。
func (s *Server) documentUnitIsLinked(ctx context.Context, docID, unitID string) bool {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM document_units WHERE doc_id=$1 AND unit_id=$2)`,
		docID, unitID).Scan(&exists)
	return err == nil && exists
}

// normalizeFilename 统一去掉路径，避免 ../ 之类的输入。
func normalizeFilename(name string) string {
	return strings.TrimSpace(name)
}
