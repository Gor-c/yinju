package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// loadEnvFile 把 .env 的内容加载到进程环境变量，返回写入的变量个数。
//
// 规则：
//   - 文件不存在不算错误（found=false），线上由 systemd/容器注入配置时无需 .env。
//   - 已存在的环境变量不会被覆盖，因此外部注入的配置优先级高于 .env。
//   - 支持 export 前缀、# 注释、单引号（原样保留）、双引号（支持 \n \t \r \\ \" 转义）。
//   - 未加引号的值会去掉行尾 " #..." 注释。
func loadEnvFile(path string) (loaded int, found bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		key, value, ok, parseErr := parseEnvLine(scanner.Text())
		if parseErr != nil {
			return loaded, true, fmt.Errorf("%s:%d: %w", path, lineNo, parseErr)
		}
		if !ok {
			continue
		}
		// 外部环境变量优先，避免 .env 覆盖部署时注入的配置。
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return loaded, true, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		loaded++
	}
	if err := scanner.Err(); err != nil {
		return loaded, true, err
	}
	return loaded, true, nil
}

// parseEnvLine 解析单行 KEY=VALUE，ok=false 表示空行或注释行。
func parseEnvLine(raw string) (key, value string, ok bool, err error) {
	line := strings.TrimPrefix(raw, "\ufeff")
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))

	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false, fmt.Errorf("invalid line %q: expected KEY=VALUE", line)
	}
	key = strings.TrimSpace(line[:eq])
	if key == "" {
		return "", "", false, fmt.Errorf("invalid line %q: empty key", line)
	}
	value = strings.TrimSpace(line[eq+1:])

	if len(value) >= 2 {
		switch value[0] {
		case '\'':
			if value[len(value)-1] != '\'' {
				return "", "", false, fmt.Errorf("invalid line %q: unterminated single quote", line)
			}
			return key, value[1 : len(value)-1], true, nil
		case '"':
			if value[len(value)-1] != '"' {
				return "", "", false, fmt.Errorf("invalid line %q: unterminated double quote", line)
			}
			return key, unescapeDoubleQuoted(value[1 : len(value)-1]), true, nil
		}
	}

	// 未加引号：去掉行尾注释（# 前必须有空白，避免截断含 # 的值）。
	if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return key, value, true, nil
}

func unescapeDoubleQuoted(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i == len(v)-1 {
			b.WriteByte(v[i])
			continue
		}
		i++
		switch v[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(v[i])
		}
	}
	return b.String()
}
