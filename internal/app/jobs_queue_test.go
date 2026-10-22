package app

import (
	"path/filepath"
	"testing"
)

func TestJobSourcePathPrefersStoredPath(t *testing.T) {
	s := testServer()
	stored := jobView{ID: "job-1", UnitID: "unit-1", Filename: "预案.docx", Path: "/var/data/unit-1/job-1_预案.docx"}
	if got := s.jobSourcePath(stored); got != stored.Path {
		t.Fatalf("jobSourcePath = %q, want stored path %q", got, stored.Path)
	}
}

func TestJobSourcePathFallbackForLegacyRows(t *testing.T) {
	t.Setenv("UPLOAD_DIR", "./data/uploads")
	s := testServer()

	// 旧数据没有 file_path，按 <UPLOAD_DIR>/<unit_id>/<job_id>_<filename> 回推。
	legacy := jobView{ID: "1792467611877061000", UnitID: "unit-e99f2bfaaf65ff01", Filename: "预案.docx"}
	want := filepath.Join("./data/uploads", "unit-e99f2bfaaf65ff01", "1792467611877061000_预案.docx")
	if got := s.jobSourcePath(legacy); got != want {
		t.Fatalf("jobSourcePath = %q, want %q", got, want)
	}
}

func TestJobSourcePathSanitizesFilename(t *testing.T) {
	t.Setenv("UPLOAD_DIR", "/data/uploads")
	s := testServer()

	// 文件名拼接必须用 Base，避免路径穿越。
	evil := jobView{ID: "job-2", UnitID: "../..", Filename: "../../etc/passwd"}
	want := filepath.Join("/data/uploads", "..", "job-2_passwd")
	if got := s.jobSourcePath(evil); got != want {
		t.Fatalf("jobSourcePath = %q, want %q", got, want)
	}
}
