package app

import (
	"strings"
	"testing"
)

// DSN 拆装是「改数据库配置」这条路上唯一的手工字符串处理，
// 密码里带 @ : / ? # 时最容易被拼坏，且症状是「连接被拒绝」而不是「格式错误」，
// 很难往这里想。所以逐种字符都要过一遍。
func TestDSNRoundTrip(t *testing.T) {
	passwords := []string{
		"changeme",
		"p@ssw0rd",
		"a:b/c?d#e",
		"带中文的密码",
		`quote'inside`,
	}
	for _, pw := range passwords {
		t.Run(pw, func(t *testing.T) {
			dsn := buildDSN(dbSettings{
				Host: "127.0.0.1", Port: "5432", Database: "fire_archive",
				User: "postgres", SSLMode: "disable",
			}, pw)

			got := parseDSN(dsn)
			if got.Host != "127.0.0.1" || got.Port != "5432" ||
				got.Database != "fire_archive" || got.User != "postgres" {
				t.Fatalf("解析后字段不一致: %+v", got)
			}
			if !got.PasswordSet {
				t.Fatal("应识别出密码已设置")
			}
			// 密码本身不参与回传，用 currentPassword 的同一段逻辑取回来比对。
			if back := passwordFromDSN(dsn); back != pw {
				t.Fatalf("密码往返丢失: got %q, want %q", back, pw)
			}
			// 脱敏串里不能出现明文。
			if strings.Contains(got.DSNMasked, pw) {
				t.Fatalf("脱敏 DSN 泄漏了密码: %s", got.DSNMasked)
			}
		})
	}
}

func TestParseDSNEmpty(t *testing.T) {
	got := parseDSN("")
	if got.PasswordSet {
		t.Fatal("空 DSN 不应标记有密码")
	}
	if got.Port != "5432" {
		t.Fatalf("空 DSN 的默认端口应为 5432，实际 %q", got.Port)
	}
	// 未配置时保持为空，页面据此显示「未配置」。
	// 这里造一个占位 DSN 反而会让人以为已经配好了。
	if got.DSNMasked != "" {
		t.Fatalf("空 DSN 不应产生脱敏串，实际 %q", got.DSNMasked)
	}
}

func TestParseDSNKeepsSSLMode(t *testing.T) {
	got := parseDSN("postgresql://u:p@db.internal:6543/app?sslmode=require")
	if got.SSLMode != "require" {
		t.Fatalf("sslmode = %q, want require", got.SSLMode)
	}
	if got.Host != "db.internal" || got.Port != "6543" {
		t.Fatalf("host/port 解析错误: %+v", got)
	}
}

// 写盘用的引号规则：单引号包裹，值里含单引号时改用双引号。
// 写坏一个引号会让整个配置文件解析失败，进而服务起不来。
func TestQuoteEnvValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "'plain'"},
		{"http://x:1/y", "'http://x:1/y'"},
		{`has'quote`, `"has'quote"`},
	}
	for _, tc := range cases {
		if got := quoteEnvValue(tc.in); got != tc.want {
			t.Fatalf("quoteEnvValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// 页面读到的「来源」依赖这个解析器；它必须能跳过注释、
// 处理 export 前缀与两种引号，否则来源判断会整体偏移。
func TestParseEnvText(t *testing.T) {
	raw := `# 注释
PG_DSN='postgresql://u:p@h:5432/db'
export VECTOR_STORE=chroma
CHROMA_URL="http://127.0.0.1:8000"

BAD LINE WITHOUT EQUALS
`
	got := parseEnvText(raw)
	if got["PG_DSN"] != "postgresql://u:p@h:5432/db" {
		t.Fatalf("PG_DSN = %q", got["PG_DSN"])
	}
	if got["VECTOR_STORE"] != "chroma" {
		t.Fatalf("export 前缀未处理: %q", got["VECTOR_STORE"])
	}
	if got["CHROMA_URL"] != "http://127.0.0.1:8000" {
		t.Fatalf("双引号未去除: %q", got["CHROMA_URL"])
	}
	if len(got) != 3 {
		t.Fatalf("异常行不应产生键，实际解析出 %d 个", len(got))
	}
}
