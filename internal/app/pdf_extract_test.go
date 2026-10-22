package app

import (
	"os"
	"strings"
	"testing"
)

func TestPDFTextRequiresReadableFile(t *testing.T) {
	_, err := pdfText("/nonexistent/definitely-missing.pdf")
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPDFTotextExtractMissingBinary(t *testing.T) {
	t.Setenv("PDFTOTEXT_BIN", "/nonexistent/pdftotext")
	if _, err := pdftotextExtract("/nonexistent/a.pdf"); err == nil {
		t.Fatal("expected an error when the binary is missing")
	}
}

// 用真实 PDF 验证解析效果：
//
//	PDF_TEST_FILE=/path/to/text.pdf go test ./internal/app -run TestPDFExtractIntegration -v
//
// 扫描件会返回「未提取到文本」，文本型 PDF 会打印前 200 字供人工确认。
func TestPDFExtractIntegration(t *testing.T) {
	path := os.Getenv("PDF_TEST_FILE")
	if path == "" {
		t.Skip("set PDF_TEST_FILE to a PDF path")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("PDF_TEST_FILE not readable: %v", err)
	}

	text, err := goPDFText(path)
	if err != nil {
		t.Logf("go parser returned error: %v", err)
	} else {
		runes := []rune(strings.TrimSpace(text))
		if len(runes) > 200 {
			runes = runes[:200]
		}
		t.Logf("go parser extracted %d runes: %q", len([]rune(text)), string(runes))
	}

	full, err := pdfText(path)
	if err != nil {
		t.Logf("pdfText (with fallback) returned error: %v", err)
		return
	}
	t.Logf("pdfText extracted %d runes", len([]rune(full)))
}
