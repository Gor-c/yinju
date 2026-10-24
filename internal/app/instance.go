package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
)

// 实例级配置：数据库、向量库、运行目录。
//
// 与「检索参数」「品牌」不同，这些值决定进程怎么起来，改完必须重启才生效。
// 因此这里刻意不做热更新，只做「看清楚 + 改文件 + 校验 + 留痕」。
//
// 最重要的一条约束：**本文件的所有 handler 都不依赖 s.db**。
// 如果 DSN 改错，重启后连不上数据库；此时页面必须仍然能打开、能改回去，
// 否则就是死锁——配置存在它要连接的库里，错一次就只能去服务器上改文件。

const (
	instanceEnvFileKey = "INSTANCE_ENV_FILE"
	defaultInstanceEnv = "./data/instance.env"
)

// 页面管理的键，固定顺序写盘，便于人工审阅 diff。
var instanceKeys = []string{
	"PG_DSN",
	"VECTOR_STORE",
	"CHROMA_URL",
	"CHROMA_TENANT",
	"CHROMA_DATABASE",
	"CHROMA_COLLECTION",
	"LISTEN_ADDR",
	"UPLOAD_DIR",
	"BRAND_DIR",
}

// 各键在页面上的默认值，与代码里的实际默认保持一致。
var instanceDefaults = map[string]string{
	"VECTOR_STORE":      "pgvector",
	"CHROMA_URL":        "http://127.0.0.1:8000",
	"CHROMA_TENANT":     "default_tenant",
	"CHROMA_DATABASE":   "default_database",
	"CHROMA_COLLECTION": "knowledge_base",
	"UPLOAD_DIR":        "./data/uploads",
	"BRAND_DIR":         "./data/brand",
}

type dbSettings struct {
	Host        string `json:"host"`
	Port        string `json:"port"`
	Database    string `json:"database"`
	User        string `json:"user"`
	SSLMode     string `json:"sslmode"`
	PasswordSet bool   `json:"password_set"`
	// DSNMasked 供人工核对：密码换成 ***，其余原样。明文永远不出服务端。
	DSNMasked string `json:"dsn_masked"`
}

type vectorSettings struct {
	Store      string `json:"store"`
	URL        string `json:"url"`
	Tenant     string `json:"tenant"`
	Database   string `json:"database"`
	Collection string `json:"collection"`
}

type runtimeSettingsView struct {
	ListenAddr string `json:"listen_addr"`
	UploadDir  string `json:"upload_dir"`
	BrandDir   string `json:"brand_dir"`
}

type instanceSnapshot struct {
	ConfigFile string `json:"config_file"`
	// EnvFile 是兜底的 .env，页面上标出来，用户才知道值从哪来。
	EnvFile   string `json:"env_file"`
	Saved     bool   `json:"saved"`
	SavedAt   string `json:"saved_at,omitempty"`
	HasBackup bool   `json:"has_backup"`

	Database dbSettings          `json:"database"`
	Vector   vectorSettings      `json:"vector"`
	Runtime  runtimeSettingsView `json:"runtime"`

	// Sources: key → instance | env_file | default | external
	Sources map[string]string `json:"sources"`
	// Pending 是「已保存但当前进程还没用上」的键。
	Pending []string `json:"pending"`
	// Locked 是被外部环境变量占住、页面改了也不会生效的键。
	Locked []string `json:"locked"`
}

func instanceEnvPath() string {
	return env(instanceEnvFileKey, defaultInstanceEnv)
}

func envFilePath() string {
	return env("ENV_FILE", ".env")
}

// ---------- 文件读写 ----------

// parseEnvText 是只读用的宽松解析：读不懂的行直接跳过，
// 这里的目的只是「知道某个键在文件里是什么值」，不是校验文件合法性。
func parseEnvText(raw string) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		if len(value) >= 2 {
			if (value[0] == '\'' && value[len(value)-1] == '\'') ||
				(value[0] == '"' && value[len(value)-1] == '"') {
				value = value[1 : len(value)-1]
			}
		}
		out[key] = value
	}
	return out
}

func readEnvFileMap(path string) map[string]string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	return parseEnvText(string(raw))
}

// quoteEnvValue 用单引号包裹，遇到单引号改双引号。
// 这两个分支覆盖了本项目所有实际值（DSN 里的密码会被 URL 编码）。
func quoteEnvValue(v string) string {
	if strings.Contains(v, "'") {
		return `"` + strings.ReplaceAll(v, `\`, `\\`) + `"`
	}
	return "'" + v + "'"
}

// writeInstanceEnv 原子写：先写临时文件再 rename，
// 避免进程在写一半时被重启留下半截配置。
func writeInstanceEnv(values map[string]string) error {
	path := instanceEnvPath()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}

	// 备份上一版：改错之后至少有一个可以回滚的锚点。
	if fileExists(path) {
		if raw, err := os.ReadFile(path); err == nil {
			if err := os.WriteFile(path+".bak", raw, 0600); err != nil {
				logging.App().Warn("instance_env_backup_failed", "error", err)
			}
		}
	}

	var b strings.Builder
	b.WriteString("# 由「设置 → 实例配置」写入，也可以手工编辑。\n")
	b.WriteString("# 优先级：外部环境变量 > 本文件 > .env > 代码默认值。\n")
	b.WriteString("# 改动需要重启服务才生效。\n")
	for _, key := range instanceKeys {
		value, ok := values[key]
		if !ok || value == "" {
			continue
		}
		b.WriteString(key + "=" + quoteEnvValue(value) + "\n")
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------- DSN 拆装 ----------

// parseDSN 只处理 URL 形式（本项目统一用 postgresql://）。
// 解析失败时返回空结构：页面上会显示为「未配置」，而不是报错挡住整页。
func parseDSN(dsn string) dbSettings {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return dbSettings{Port: "5432", SSLMode: "disable"}
	}
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return dbSettings{Port: "5432", SSLMode: "disable"}
	}
	out := dbSettings{
		Host:     u.Hostname(),
		Port:     u.Port(),
		Database: strings.TrimPrefix(u.Path, "/"),
		SSLMode:  u.Query().Get("sslmode"),
	}
	if out.Port == "" {
		out.Port = "5432"
	}
	if out.SSLMode == "" {
		out.SSLMode = "disable"
	}
	if u.User != nil {
		out.User = u.User.Username()
		if _, ok := u.User.Password(); ok {
			out.PasswordSet = true
		}
	}
	out.DSNMasked = maskDSN(out, out.PasswordSet)
	return out
}

func maskDSN(s dbSettings, hasPassword bool) string {
	user := s.User
	if user == "" {
		user = "user"
	}
	secret := ""
	if hasPassword {
		secret = ":***"
	}
	host := s.Host
	if host == "" {
		host = "host"
	}
	dbName := s.Database
	if dbName == "" {
		dbName = "database"
	}
	return fmt.Sprintf("postgresql://%s%s@%s:%s/%s?sslmode=%s", user, secret, host, s.Port, dbName, s.SSLMode)
}

func buildDSN(s dbSettings, password string) string {
	u := &url.URL{
		Scheme: "postgresql",
		Host:   net.JoinHostPort(s.Host, s.Port),
		Path:   "/" + s.Database,
	}
	if s.User != "" {
		if password != "" {
			u.User = url.UserPassword(s.User, password)
		} else {
			u.User = url.User(s.User)
		}
	}
	q := url.Values{}
	if s.SSLMode != "" {
		q.Set("sslmode", s.SSLMode)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ---------- 读取 ----------

// loadInstance 汇总三处来源：instance.env、.env、外部环境变量。
// 返回值里同时给出「下一个进程会用什么」和「当前进程在用的是什么」，
// 页面才能说清楚「改了但还没重启」这件事。
func (s *Server) loadInstance() instanceSnapshot {
	saved := readEnvFileMap(instanceEnvPath())
	fallback := readEnvFileMap(envFilePath())

	snap := instanceSnapshot{
		ConfigFile: instanceEnvPath(),
		EnvFile:    envFilePath(),
		Saved:      len(saved) > 0,
		HasBackup:  fileExists(instanceEnvPath() + ".bak"),
		Sources:    map[string]string{},
	}
	if info, err := os.Stat(instanceEnvPath()); err == nil {
		snap.SavedAt = info.ModTime().Format(time.RFC3339)
	}

	// resolve 决定某个键的生效值，并记录它的来源。
	// 外部环境变量优先级最高，但「外部」的判定要排除本文件和 .env，
	// 否则一个刚保存、还没重启的值会被误判成外部注入。
	resolve := func(key string) string {
		fileValue, inSaved := saved[key]
		envValue, inEnvFile := fallback[key]
		procValue, inProc := os.LookupEnv(key)

		switch {
		case inSaved:
			snap.Sources[key] = "instance"
			if inProc && procValue != fileValue {
				snap.Pending = append(snap.Pending, key)
			}
			return fileValue
		case inEnvFile:
			if inProc && procValue != envValue {
				// 值来自外部注入，页面改不动它。
				snap.Sources[key] = "external"
				snap.Locked = append(snap.Locked, key)
				return procValue
			}
			snap.Sources[key] = "env_file"
			return envValue
		case inProc && procValue != "":
			snap.Sources[key] = "external"
			snap.Locked = append(snap.Locked, key)
			return procValue
		default:
			snap.Sources[key] = "default"
			return instanceDefaults[key]
		}
	}

	dsn := resolve("PG_DSN")
	snap.Database = parseDSN(dsn)
	// 空 DSN 时 parseDSN 给的是占位值，这里把来源标出来供页面提示。
	if dsn == "" {
		snap.Sources["PG_DSN"] = "default"
	}

	snap.Vector = vectorSettings{
		Store:      resolve("VECTOR_STORE"),
		URL:        resolve("CHROMA_URL"),
		Tenant:     resolve("CHROMA_TENANT"),
		Database:   resolve("CHROMA_DATABASE"),
		Collection: resolve("CHROMA_COLLECTION"),
	}

	listen := resolve("LISTEN_ADDR")
	if listen == "" {
		// 与 main.go 的推导保持一致：没写 LISTEN_ADDR 时由 PORT 拼出来。
		listen = "127.0.0.1:" + env("PORT", "8080")
		snap.Sources["LISTEN_ADDR"] = "default"
	}
	snap.Runtime = runtimeSettingsView{
		ListenAddr: listen,
		UploadDir:  resolve("UPLOAD_DIR"),
		BrandDir:   resolve("BRAND_DIR"),
	}

	sort.Strings(snap.Pending)
	sort.Strings(snap.Locked)
	return snap
}

func (s *Server) instanceConfig(c *gin.Context) {
	// 刻意不调用 s.ready：数据库连不上时，这个页面必须还能打开。
	c.JSON(http.StatusOK, s.loadInstance())
}

// ---------- 保存 ----------

type instanceInput struct {
	Host     *string `json:"host"`
	Port     *string `json:"port"`
	Database *string `json:"database"`
	User     *string `json:"user"`
	// Password 留空表示「不修改」——页面上从不回传明文，
	// 用空值代表保留，是唯一不会把密码清空的语义。
	Password *string `json:"password"`
	SSLMode  *string `json:"sslmode"`

	VectorStore      *string `json:"vector_store"`
	ChromaURL        *string `json:"chroma_url"`
	ChromaTenant     *string `json:"chroma_tenant"`
	ChromaDatabase   *string `json:"chroma_database"`
	ChromaCollection *string `json:"chroma_collection"`

	ListenAddr *string `json:"listen_addr"`
	UploadDir  *string `json:"upload_dir"`
	BrandDir   *string `json:"brand_dir"`

	// Force 跳过保存前的连通性检查。数据库还没起来、
	// 或者要先写好配置再启动数据库时，需要这条路。
	Force bool `json:"force"`
}

func (s *Server) saveInstance(c *gin.Context) {
	var in instanceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	current := s.loadInstance()
	next := current.Database

	if in.Host != nil {
		next.Host = strings.TrimSpace(*in.Host)
	}
	if in.Port != nil {
		next.Port = strings.TrimSpace(*in.Port)
	}
	if in.Database != nil {
		next.Database = strings.TrimSpace(*in.Database)
	}
	if in.User != nil {
		next.User = strings.TrimSpace(*in.User)
	}
	if in.SSLMode != nil {
		next.SSLMode = strings.TrimSpace(*in.SSLMode)
	}
	if next.SSLMode == "" {
		next.SSLMode = "disable"
	}
	if next.Port == "" {
		next.Port = "5432"
	}

	var missing []string
	if next.Host == "" {
		missing = append(missing, "主机")
	}
	if next.Database == "" {
		missing = append(missing, "数据库名")
	}
	if next.User == "" {
		missing = append(missing, "用户名")
	}
	if len(missing) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写：" + strings.Join(missing, "、")})
		return
	}
	if _, err := net.LookupPort("tcp", next.Port); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "端口不是有效数字：" + next.Port})
		return
	}

	// 密码留空表示沿用原值；原值不在页面回传，只能从当前 DSN 里取。
	password := ""
	if in.Password != nil {
		password = *in.Password
	}
	if password == "" && current.Database.PasswordSet {
		password = currentPassword()
	}

	values := map[string]string{
		"PG_DSN": buildDSN(next, password),
	}

	pick := func(ptr *string, fallback string) string {
		if ptr != nil {
			return strings.TrimSpace(*ptr)
		}
		return fallback
	}
	values["VECTOR_STORE"] = pick(in.VectorStore, current.Vector.Store)
	values["CHROMA_URL"] = pick(in.ChromaURL, current.Vector.URL)
	values["CHROMA_TENANT"] = pick(in.ChromaTenant, current.Vector.Tenant)
	values["CHROMA_DATABASE"] = pick(in.ChromaDatabase, current.Vector.Database)
	values["CHROMA_COLLECTION"] = pick(in.ChromaCollection, current.Vector.Collection)
	values["LISTEN_ADDR"] = pick(in.ListenAddr, current.Runtime.ListenAddr)
	values["UPLOAD_DIR"] = pick(in.UploadDir, current.Runtime.UploadDir)
	values["BRAND_DIR"] = pick(in.BrandDir, current.Runtime.BrandDir)

	if values["VECTOR_STORE"] != "chroma" && values["VECTOR_STORE"] != "pgvector" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "向量库只能是 chroma 或 pgvector"})
		return
	}
	if cur := values["CHROMA_URL"]; values["VECTOR_STORE"] == "chroma" {
		if _, err := url.Parse(cur); err != nil || !strings.HasPrefix(cur, "http") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Chroma 地址需要是 http(s):// 开头的 URL"})
			return
		}
	}

	// 保存前实测一次：连不上就直接拦下，这是最便宜的一道保险。
	// 用户确实要先写配置再启库时，用 force 绕过。
	if !in.Force {
		if err := testDatabaseDSN(c.Request.Context(), values["PG_DSN"]); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":     "新配置连不上数据库，未保存：" + err.Error(),
				"can_force": true,
				"hint":      "确认数据库地址无误后重试；如果数据库还没启动，可以选择「仍然保存」。",
			})
			return
		}
	}

	if err := writeInstanceEnv(values); err != nil {
		logging.App().Warn("instance_env_write_failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入配置文件失败：" + err.Error()})
		return
	}

	logging.App().Info("instance_config_saved",
		"path", instanceEnvPath(),
		"db_host", next.Host, "db_port", next.Port, "db_name", next.Database,
		"vector_store", values["VECTOR_STORE"], "force", in.Force)

	c.JSON(http.StatusOK, gin.H{
		"snapshot":         s.loadInstance(),
		"restart_required": true,
	})
}

// passwordFromDSN 取出 DSN 里的密码。
// 页面上密码框留空表示「不修改」，此时必须靠这个函数把原密码找回来，
// 否则保存一次端口就会把密码清空。
func passwordFromDSN(dsn string) string {
	u, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || u.User == nil {
		return ""
	}
	pass, _ := u.User.Password()
	return pass
}

// currentPassword 取当前进程生效的密码。全程不出服务端，也不写进任何响应。
func currentPassword() string {
	return passwordFromDSN(os.Getenv("PG_DSN"))
}

// ---------- 连通性测试 ----------

func testDatabaseDSN(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return simplifyPgError(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return simplifyPgError(err)
	}
	return nil
}

// simplifyPgError 把 pgx 的长错误压成一句能读懂的话。
// 原始错误在日志里仍然完整，这里只负责让页面上那句提示有用。
func simplifyPgError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return errors.New("连接被拒绝，检查主机与端口，以及数据库是否已启动")
	case strings.Contains(msg, "password authentication failed"):
		return errors.New("用户名或密码不正确")
	case strings.Contains(msg, "does not exist"):
		return errors.New("数据库不存在")
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		return errors.New("连接超时，检查网络或防火墙")
	case strings.Contains(msg, "no such host"):
		return errors.New("主机名无法解析")
	}
	return errors.New(msg)
}

type testDBInput struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

func (s *Server) testInstanceDatabase(c *gin.Context) {
	var in testDBInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}
	password := in.Password
	if password == "" {
		// 页面上密码框留空时不回传明文，这里补上当前生效的密码，
		// 否则「只改端口」也会因为密码为空而测试失败。
		password = currentPassword()
	}
	dsn := buildDSN(dbSettings{
		Host: strings.TrimSpace(in.Host), Port: orDefault(in.Port, "5432"),
		Database: strings.TrimSpace(in.Database), User: strings.TrimSpace(in.User),
		SSLMode: orDefault(in.SSLMode, "disable"),
	}, password)

	started := time.Now()
	if err := testDatabaseDSN(c.Request.Context(), dsn); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": err.Error(), "elapsed_ms": time.Since(started).Milliseconds()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "elapsed_ms": time.Since(started).Milliseconds()})
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return strings.TrimSpace(v)
}

// testInstanceVector 检查向量库是否可达。
// chroma 走心跳接口；pgvector 复用数据库连接——它没有独立的服务。
func (s *Server) testInstanceVector(c *gin.Context) {
	var in struct {
		Store    string     `json:"store"`
		URL      string     `json:"url"`
		Database dbSettings `json:"database"`
		Password string     `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	started := time.Now()
	if in.Store == "pgvector" {
		password := in.Password
		if password == "" {
			password = currentPassword()
		}
		dsn := buildDSN(in.Database, password)
		if err := testDatabaseDSN(c.Request.Context(), dsn); err != nil {
			c.JSON(http.StatusOK, gin.H{"ok": false, "error": "pgvector 依赖数据库：" + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "detail": "pgvector 使用当前数据库连接", "elapsed_ms": time.Since(started).Milliseconds()})
		return
	}

	base := strings.TrimRight(strings.TrimSpace(in.URL), "/")
	if base == "" {
		base = instanceDefaults["CHROMA_URL"]
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v2/heartbeat", nil)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": "地址格式不正确"})
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": "无法连接：" + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": fmt.Sprintf("服务返回 %d", resp.StatusCode)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "elapsed_ms": time.Since(started).Milliseconds()})
}

// ---------- 回滚 ----------

// rollbackInstance 用备份覆盖当前配置。
// 这是「改错了、服务起不来」时唯一的自救手段，所以它同样不依赖数据库。
func (s *Server) rollbackInstance(c *gin.Context) {
	path := instanceEnvPath()
	backup := path + ".bak"
	if !fileExists(backup) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有可回滚的备份"})
		return
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取备份失败：" + err.Error()})
		return
	}
	// 先把当前这版也留一份，回滚本身也可能是错的。
	if cur, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".prev", cur, 0600)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写回失败：" + err.Error()})
		return
	}
	logging.App().Info("instance_config_rolled_back", "path", path)
	c.JSON(http.StatusOK, gin.H{"snapshot": s.loadInstance(), "restart_required": true})
}
