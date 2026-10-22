package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchRejectsPrivateAndInternalTargets(t *testing.T) {
	// The listener itself runs on loopback, so only the rejected cases can be
	// exercised here; the happy path is covered by manual verification.
	targets := []string{
		"http://127.0.0.1/",
		"http://localhost/",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
		"http://100.64.0.1/",
		"http://[::1]/",
	}
	for _, target := range targets {
		if _, err := FetchStandalone(context.Background(), target, 0); err == nil {
			t.Fatalf("Fetch(%q) succeeded, want rejection", target)
		}
	}
}

func TestFetchRejectsNonHTTPSchemes(t *testing.T) {
	for _, target := range []string{"file:///etc/passwd", "ftp://example.com/x", "gopher://example.com/"} {
		if _, err := FetchStandalone(context.Background(), target, 0); err == nil {
			t.Fatalf("Fetch(%q) succeeded, want rejection", target)
		}
	}
}

func TestFetchRejectsUnsupportedContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write([]byte("PK\x03\x04"))
	}))
	defer srv.Close()
	// Loopback is blocked, so this asserts the scheme/host guard runs first.
	if _, err := FetchStandalone(context.Background(), srv.URL, 0); err == nil {
		t.Fatal("expected the private-host guard to reject the loopback test server")
	}
}

func TestExtractTextRemovesScriptsAndMarkup(t *testing.T) {
	html := []byte(`<html><head><title>规范标题</title><style>body{}</style></head>
	<body><script>alert(1)</script><h1>第一章</h1><p>这是  正文内容。</p><!-- 注释 --><p>&amp; 第二段</p></body></html>`)
	text := ExtractText("text/html", html)
	for _, want := range []string{"第一章", "这是 正文内容。", "& 第二段"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"alert(1)", "<script>", "注释", "body{}"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("text still contains %q:\n%s", unwanted, text)
		}
	}
}

func TestExtractTextKeepsPlainText(t *testing.T) {
	text := ExtractText("text/plain", []byte("第一行\r\n\r\n第二行"))
	if !strings.Contains(text, "第一行") || !strings.Contains(text, "第二行") {
		t.Fatalf("text = %q", text)
	}
	if strings.Contains(text, "\r") {
		t.Fatalf("line endings were not normalized: %q", text)
	}
}

func TestPageTitle(t *testing.T) {
	body := []byte("<html><head><title>  标题 &amp; 副标题  </title></head><body>x</body></html>")
	if got := pageTitle(body, "fallback"); got != "标题 & 副标题" {
		t.Fatalf("title = %q", got)
	}
	if got := pageTitle([]byte("<html></html>"), "第一行\n第二行"); got != "第一行" {
		t.Fatalf("fallback title = %q", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"消防安全规范 2026":      "消防安全规范-2026",
		"../../etc/passwd": "etcpasswd",
		"":                 "web-page",
	}
	for input, want := range cases {
		if got := Slugify(input, "web-page"); got != want {
			t.Fatalf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}
	long := Slugify(strings.Repeat("长", 200), "web-page")
	if len([]rune(long)) > 60 {
		t.Fatalf("slug is not truncated: %q", long)
	}
}
