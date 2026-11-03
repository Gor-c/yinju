package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// newMockGithub 起一个本地 API，覆盖 tree 与 contents 两个端点。
func newMockGithub(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/docs/git/trees/", func(w http.ResponseWriter, r *http.Request) {
		tree := make([]map[string]string, 0, len(files))
		for path := range files {
			tree = append(tree, map[string]string{"path": path, "type": "blob"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tree": tree, "truncated": false})
	})
	mux.HandleFunc("/repos/acme/docs/contents/", func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/repos/acme/docs/contents/")
		content, ok := files[rel]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content":  base64.StdEncoding.EncodeToString([]byte(content)),
			"encoding": "base64",
			"size":     len(content),
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func withGithubBase(t *testing.T, base string) {
	t.Helper()
	old, had := os.LookupEnv("GITHUB_API_BASE")
	if err := os.Setenv("GITHUB_API_BASE", base); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("GITHUB_API_BASE", old)
			return
		}
		_ = os.Unsetenv("GITHUB_API_BASE")
	})
}

func TestListGithubFilesFiltersNonText(t *testing.T) {
	server := newMockGithub(t, map[string]string{
		"docs/intro.md":    "# 介绍",
		"docs/plan.MD":     "# 计划",
		"docs/logo.png":    "binary",
		"src/main.go":      "package main",
		"docs/guide/a.txt": "文本",
	})
	withGithubBase(t, server.URL)

	files, err := listGithubFiles(context.Background(), "", "acme", "docs", "main", "docs", 10)
	if err != nil {
		t.Fatalf("listGithubFiles: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 text files, got %v", files)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, "docs/") {
			t.Fatalf("path filter failed: %s", f.Path)
		}
	}
}

func TestListGithubFilesRespectsLimit(t *testing.T) {
	server := newMockGithub(t, map[string]string{
		"a.md": "1", "b.md": "2", "c.md": "3",
	})
	withGithubBase(t, server.URL)

	files, err := listGithubFiles(context.Background(), "", "acme", "docs", "main", "", 2)
	if err != nil {
		t.Fatalf("listGithubFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("limit not applied: %v", files)
	}
}

func TestDownloadGithubFileDecodesBase64(t *testing.T) {
	server := newMockGithub(t, map[string]string{"docs/intro.md": "# 国产化改造"})
	withGithubBase(t, server.URL)

	content, err := downloadGithubFile(context.Background(), "", "acme", "docs", "main", "docs/intro.md")
	if err != nil {
		t.Fatalf("downloadGithubFile: %v", err)
	}
	if string(content) != "# 国产化改造" {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestDownloadGithubFileMissing(t *testing.T) {
	server := newMockGithub(t, map[string]string{})
	withGithubBase(t, server.URL)

	if _, err := downloadGithubFile(context.Background(), "", "acme", "docs", "main", "docs/none.md"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSanitizeImportName(t *testing.T) {
	cases := map[string]string{
		"docs/intro.md": "docs_intro.md",
		"第一章 概述":        "第一章_概述.md",
		"a:b*c?.md":     "a_b_c_.md",
		"":              "",
	}
	for in, want := range cases {
		if got := sanitizeImportName(in); got != want {
			t.Fatalf("sanitizeImportName(%q) = %q, want %q", in, got, want)
		}
	}
}
