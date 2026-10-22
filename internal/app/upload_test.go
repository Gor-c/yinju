package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		size    int
		overlap int
		want    []string
	}{
		{name: "empty", text: "   ", size: 10, overlap: 2, want: nil},
		{name: "shorter than size", text: "知识库档案", size: 10, overlap: 2, want: []string{"知识库档案"}},
		{name: "overlap", text: "abc", size: 2, overlap: 1, want: []string{"ab", "bc"}},
		{name: "unicode runes", text: "一二三四五六", size: 4, overlap: 2, want: []string{"一二三四", "三四五六"}},
		{name: "full coverage", text: "0123456789", size: 4, overlap: 1, want: []string{"0123", "3456", "6789"}},
		{name: "invalid size", text: "abc", size: 0, overlap: 0, want: nil},
		{name: "overlap not smaller than size", text: "abcdefgh", size: 4, overlap: 9, want: []string{"abcd", "defg", "gh"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := split(tc.text, tc.size, tc.overlap)
			if len(got) != len(tc.want) {
				t.Fatalf("split() = %#v, want %#v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("split()[%d] = %q, want %q (all: %#v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestSplitCoversEveryRune(t *testing.T) {
	text := strings.Repeat("知识库文档", 400)
	chunks := split(text, chunkSize, chunkOverlap)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if n := len([]rune(chunk)); n > chunkSize {
			t.Fatalf("chunk %d has %d runes, limit is %d", i, n, chunkSize)
		}
	}
	runes := []rune(text)
	if !strings.HasSuffix(chunks[len(chunks)-1], string(runes[len(runes)-1])) {
		t.Fatalf("last chunk does not cover the document tail: %q", chunks[len(chunks)-1])
	}
}

func TestExtractTextFormats(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"制度.txt":      "txt 内容",
		"台账.csv":      "a,b\n1,2\n",
		"说明.md":       "# 标题",
		"审核.json":     `{"score":90}`,
		"页面.html":     "<p>html</p>",
		"预案.markdown": "markdown",
	}
	for name, content := range cases {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := extract(path)
		if err != nil {
			t.Fatalf("extract(%s): %v", name, err)
		}
		if got != content {
			t.Fatalf("extract(%s) = %q, want %q", name, got, content)
		}
	}
}

func TestExtractUnsupported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.docx.bin")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := extract(path); err == nil || !strings.Contains(err.Error(), "unsupported document type") {
		t.Fatalf("expected unsupported type error, got %v", err)
	}
}

func TestExtractDocx(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`<w:document><w:body><w:p><w:t>知识 &amp; 管理</w:t></w:p></w:body></w:document>`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "报告.docx")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := extract(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != "知识 & 管理" {
		t.Fatalf("docxText() = %q", got)
	}
}

func TestExtractDocxMissingDocumentXML(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if _, err := zw.Create("other.xml"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "broken.docx")
	if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := extract(path); err == nil || !strings.Contains(err.Error(), "word/document.xml") {
		t.Fatalf("expected missing document.xml error, got %v", err)
	}
}

func TestValidateUpload(t *testing.T) {
	const tenMB = int64(10) << 20
	tests := []struct {
		name     string
		unit     string
		unitName string
		docType  string
		filename string
		size     int64
		max      int64
		wantCode int
		wantErr  string
	}{
		{name: "valid", unit: "u1", docType: "制度", filename: "预案.PDF", size: 10, max: tenMB},
		{name: "valid with unit name", unitName: "示例项目", docType: "制度", filename: "a.txt", size: 10, max: tenMB},
		{name: "unit name too long", unitName: strings.Repeat("单", maxUnitNameRunes+1), docType: "制度", filename: "a.txt", size: 10, max: tenMB, wantCode: 400, wantErr: "unit_name"},
		{name: "missing unit", docType: "制度", filename: "a.txt", size: 10, max: tenMB, wantCode: 400, wantErr: "unit_id or unit_name"},
		{name: "missing doc type", unit: "u1", filename: "a.txt", size: 10, max: tenMB, wantCode: 400, wantErr: "doc_type"},
		{name: "empty file", unit: "u1", docType: "制度", filename: "a.txt", max: tenMB, wantCode: 400, wantErr: "empty"},
		{name: "too large", unit: "u1", docType: "制度", filename: "a.txt", size: tenMB + 1, max: tenMB, wantCode: 413, wantErr: "upload limit"},
		{name: "unsupported type", unit: "u1", docType: "制度", filename: "scan.bin", size: 10, max: tenMB, wantCode: 415, wantErr: "unsupported"},
		{name: "no extension", unit: "u1", docType: "制度", filename: "README", size: 10, max: tenMB, wantCode: 415, wantErr: "unsupported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, err := validateUpload(tc.unit, tc.unitName, tc.docType, tc.filename, tc.size, tc.max)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
			}
			if code != tc.wantCode {
				t.Fatalf("status = %d, want %d", code, tc.wantCode)
			}
		})
	}
}
