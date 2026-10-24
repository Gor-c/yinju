package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 连真实数据库验证文档中心模型：
//
//	PG_INTEGRATION_TEST=1 PG_DSN=... go test ./internal/app -run TestDocumentSharingIntegration -v
//
// 测试会创建临时知识库并在结束时清理，不影响既有数据。
func TestDocumentSharingIntegration(t *testing.T) {
	if os.Getenv("PG_INTEGRATION_TEST") != "1" {
		t.Skip("set PG_INTEGRATION_TEST=1 and PG_DSN to use a live database")
	}
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	s := &Server{db: pool}
	s.ensureDocumentSchema(ctx)

	unitA := "unit-it-a-" + time.Now().Format("150405.000000")
	unitB := "unit-it-b-" + time.Now().Format("150405.000000")
	for _, id := range []string{unitA, unitB} {
		if _, err := pool.Exec(ctx, `INSERT INTO units(unit_id,name) VALUES($1,$2)`, id, "集成测试-"+id); err != nil {
			t.Fatalf("create unit: %v", err)
		}
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM document_units WHERE unit_id = ANY($1)`, []string{unitA, unitB})
		_, _ = pool.Exec(context.Background(), `DELETE FROM units WHERE unit_id = ANY($1)`, []string{unitA, unitB})
	}()

	text := "多知识库共享文档的集成测试正文。"
	docID := docIDForText(text)
	if len(docID) != 20 { // "doc-" + 16 hex
		t.Fatalf("unexpected doc id: %q", docID)
	}
	if docIDForText(text) != docID {
		t.Fatal("doc id must be stable for identical content")
	}

	view := jobView{ID: "job-it", UnitID: unitA, DocType: "制度", Filename: "共享文档.txt"}
	if err := s.recordDocument(ctx, view, docID, "/tmp/it-shared.txt", text, 3, "fixed"); err != nil {
		t.Fatalf("record document: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE doc_id=$1`, docID)
	}()

	// 同一份文档挂到第二个知识库下，不应该产生第二份 records。
	if err := s.linkDocumentUnit(ctx, docID, unitB); err != nil {
		t.Fatalf("link unit: %v", err)
	}
	ids, owner, err := s.docIDsForUnitsWithOwner(ctx, []string{unitA, unitB})
	if err != nil {
		t.Fatalf("doc ids: %v", err)
	}
	if len(ids) != 1 || ids[0] != docID {
		t.Fatalf("expected one shared doc, got %#v", ids)
	}
	if owner[docID] != unitA {
		t.Fatalf("owner should follow unit order, got %q", owner[docID])
	}
	var docCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE doc_id=$1`, docID).Scan(&docCount)
	if docCount != 1 {
		t.Fatalf("documents rows = %d, want 1", docCount)
	}

	// 从 A 移除：文档仍被 B 引用，必须保留。
	if err := s.unlinkDocumentUnit(ctx, docID, unitA); err != nil {
		t.Fatalf("unlink A: %v", err)
	}
	removed, err := s.cleanupOrphanDocument(ctx, docID)
	if err != nil || removed {
		t.Fatalf("doc should survive while unit B references it (removed=%v err=%v)", removed, err)
	}
	if ref, err := s.documentByID(ctx, docID); err != nil || ref.Filename != "共享文档.txt" {
		t.Fatalf("document should still exist: %#v err=%v", ref, err)
	}

	// 从 B 移除：不再有引用，连带删除文档记录。
	if err := s.unlinkDocumentUnit(ctx, docID, unitB); err != nil {
		t.Fatalf("unlink B: %v", err)
	}
	removed, err = s.cleanupOrphanDocument(ctx, docID)
	if err != nil {
		t.Fatalf("cleanup orphan: %v", err)
	}
	if !removed {
		t.Fatal("orphan document should be removed")
	}
	if _, err := s.documentByID(ctx, docID); err == nil {
		t.Fatal("document row should be gone")
	}
}

// 同一知识库下同名文件内容变化后，旧版本应被替换而不是并存两份。
func TestDocumentReplacementIntegration(t *testing.T) {
	if os.Getenv("PG_INTEGRATION_TEST") != "1" {
		t.Skip("set PG_INTEGRATION_TEST=1 and PG_DSN to use a live database")
	}
	dsn := os.Getenv("PG_DSN")
	if dsn == "" {
		t.Skip("PG_DSN is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	s := &Server{db: pool}
	s.ensureDocumentSchema(ctx)

	unitID := "unit-it-replace-" + time.Now().Format("150405.000000")
	if _, err := pool.Exec(ctx, `INSERT INTO units(unit_id,name) VALUES($1,$2)`, unitID, "替换测试"); err != nil {
		t.Fatalf("create unit: %v", err)
	}
	staleIDs := []string{}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM document_units WHERE unit_id=$1`, unitID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM units WHERE unit_id=$1`, unitID)
		for _, id := range staleIDs {
			_, _ = pool.Exec(context.Background(), `DELETE FROM documents WHERE doc_id=$1`, id)
		}
	}()

	oldID := docIDForText("第一版内容")
	newID := docIDForText("第二版内容")
	staleIDs = append(staleIDs, oldID, newID)

	view := jobView{ID: "job-r1", UnitID: unitID, DocType: "制度", Filename: "同名文档.txt"}
	if err := s.recordDocument(ctx, view, oldID, "/tmp/it-old.txt", "第一版内容", 1, "fixed"); err != nil {
		t.Fatalf("record v1: %v", err)
	}
	view.ID = "job-r2"
	if err := s.recordDocument(ctx, view, newID, "/tmp/it-new.txt", "第二版内容", 2, "fixed"); err != nil {
		t.Fatalf("record v2: %v", err)
	}

	var total, linked int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE filename='同名文档.txt' AND doc_id = ANY($1)`, staleIDs).Scan(&total)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM document_units WHERE unit_id=$1`, unitID).Scan(&linked)
	if total != 1 {
		t.Fatalf("expected only the new version to remain, got %d", total)
	}
	if linked != 1 {
		t.Fatalf("expected one link in the unit, got %d", linked)
	}
	var kept string
	_ = pool.QueryRow(ctx, `SELECT doc_id FROM document_units WHERE unit_id=$1`, unitID).Scan(&kept)
	if kept != newID {
		t.Fatalf("unit should keep the new version, got %q", kept)
	}
}

func TestDocIDIsContentAddressed(t *testing.T) {
	a := docIDForText("同样的内容")
	b := docIDForText("同样的内容")
	c := docIDForText("不同的内容")
	if a != b {
		t.Fatal("same content must produce the same doc id")
	}
	if a == c {
		t.Fatal("different content must produce different ids")
	}
	if contentHash("x") == contentHash("y") {
		t.Fatal("content hash collision")
	}
}
