package logging

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode log line %q: %v", scanner.Text(), err)
		}
		out = append(out, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func messages(records []map[string]any) []string {
	out := make([]string, 0, len(records))
	for _, record := range records {
		message, _ := record["msg"].(string)
		out = append(out, message)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestInitRoutesRecordsToFiles(t *testing.T) {
	dir := t.TempDir()
	closeLogs, err := Init(Config{Dir: dir, Format: "json", Level: "info", Console: false, MaxSizeMB: 0})
	if err != nil {
		t.Fatal(err)
	}

	App().Info("app_info", "job_id", "job-1")
	App().Error("app_error", "job_id", "job-2")
	HTTP().Info("http_request", "path", "/api/v1/units", "status", 200)
	HTTP().Error("http_error", "path", "/api/v1/fail", "status", 500)
	if err := closeLogs(); err != nil {
		t.Fatal(err)
	}

	appMessages := messages(readLines(t, filepath.Join(dir, "app.log")))
	if !contains(appMessages, "app_info") || !contains(appMessages, "app_error") {
		t.Fatalf("app.log messages = %#v", appMessages)
	}
	accessMessages := messages(readLines(t, filepath.Join(dir, "access.log")))
	if !contains(accessMessages, "http_request") || !contains(accessMessages, "http_error") {
		t.Fatalf("access.log messages = %#v", accessMessages)
	}
	errorMessages := messages(readLines(t, filepath.Join(dir, "error.log")))
	if contains(errorMessages, "app_info") || contains(errorMessages, "http_request") {
		t.Fatalf("error.log contains non-error records: %#v", errorMessages)
	}
	if !contains(errorMessages, "app_error") || !contains(errorMessages, "http_error") {
		t.Fatalf("error.log messages = %#v", errorMessages)
	}
}

func TestInitRespectsLevel(t *testing.T) {
	dir := t.TempDir()
	closeLogs, err := Init(Config{Dir: dir, Format: "text", Level: "warn", Console: false, MaxSizeMB: 0})
	if err != nil {
		t.Fatal(err)
	}
	App().Info("app_info")
	App().Warn("app_warn")
	HTTP().Info("http_info")
	if err := closeLogs(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "app_info") {
		t.Fatalf("INFO record was written at warn level: %s", raw)
	}
	if !strings.Contains(string(raw), "app_warn") {
		t.Fatalf("WARN record is missing: %s", raw)
	}
	access, err := os.ReadFile(filepath.Join(dir, "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(access) != 0 {
		t.Fatalf("access.log should be empty at warn level: %s", access)
	}
}

func TestInitRejectsUnknownLevel(t *testing.T) {
	if _, err := Init(Config{Dir: t.TempDir(), Level: "verbose"}); err == nil {
		t.Fatal("expected invalid level error")
	}
}

func TestRotateWriterRotatesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	writer, err := newRotateWriter(dir, "app", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(make([]byte, 700*1024)); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(make([]byte, 400*1024)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	rotated, err := filepath.Glob(filepath.Join(dir, "app-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) != 1 {
		t.Fatalf("rotated files = %#v, want 1 (maxBackups=1)", rotated)
	}
	info, err := os.Stat(rotated[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 700*1024 {
		t.Fatalf("rotated size = %d, want %d", info.Size(), 700*1024)
	}
	main, err := os.Stat(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	if main.Size() != 400*1024 {
		t.Fatalf("current size = %d, want %d", main.Size(), 400*1024)
	}
}
