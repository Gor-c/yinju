package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestLoadEnvFile(t *testing.T) {
	path := writeEnvFile(t, `
# comment
PLAIN=value
export EXPORTED=exported-value
SINGLE='{"a": 1, "b": [1,2]}'
DOUBLE="line1\nline2"
QUOTED_HASH="a # b"
INLINE_HASH=abc # trailing comment
EMPTY=
`)

	// 避免宿主机已有同名变量导致「不覆盖」分支生效。
	for _, key := range []string{
		"PLAIN", "EXPORTED", "SINGLE", "DOUBLE", "QUOTED_HASH", "INLINE_HASH", "EMPTY",
	} {
		os.Unsetenv(key)
		t.Cleanup(func() { os.Unsetenv(key) })
	}

	loaded, found, err := loadEnvFile(path)
	if err != nil {
		t.Fatalf("loadEnvFile: %v", err)
	}
	if !found || loaded != 7 {
		t.Fatalf("loaded=%d found=%v, want 7 true", loaded, found)
	}

	cases := map[string]string{
		"PLAIN":       "value",
		"EXPORTED":    "exported-value",
		"SINGLE":      `{"a": 1, "b": [1,2]}`,
		"DOUBLE":      "line1\nline2",
		"QUOTED_HASH": "a # b",
		"INLINE_HASH": "abc",
		"EMPTY":       "",
	}
	for key, want := range cases {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadEnvFileDoesNotOverrideExisting(t *testing.T) {
	path := writeEnvFile(t, "EXISTING=from-file\nNEWVAR=from-file\n")
	t.Setenv("EXISTING", "from-env")
	os.Unsetenv("NEWVAR")

	loaded, found, err := loadEnvFile(path)
	if err != nil {
		t.Fatalf("loadEnvFile: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if loaded != 1 {
		t.Fatalf("loaded = %d, want 1", loaded)
	}
	if got := os.Getenv("EXISTING"); got != "from-env" {
		t.Errorf("EXISTING = %q, want %q", got, "from-env")
	}
	if got := os.Getenv("NEWVAR"); got != "from-file" {
		t.Errorf("NEWVAR = %q, want %q", got, "from-file")
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	loaded, found, err := loadEnvFile(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || loaded != 0 {
		t.Fatalf("loaded=%d found=%v, want 0 false", loaded, found)
	}
}

func TestLoadEnvFileInvalidLine(t *testing.T) {
	path := writeEnvFile(t, "NO_EQUALS_SIGN\n")
	if _, _, err := loadEnvFile(path); err == nil {
		t.Fatal("expected error for invalid line")
	}
}
