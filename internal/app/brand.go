package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

// 品牌配置：名称、副标题、浏览器标题，以及自定义 logo。
//
// 之所以单独建表而不是塞进 retrieval_settings：那张表是「检索参数」，
// 改品牌不该触发它的缓存失效，也不该出现在检索留痕里。
//
// logo 的存储刻意不放 frontend/dist —— 那是构建产物，任何一次 pnpm build
// 都会把它清掉；二进制嵌入的也只是构建时刻的 dist。放在数据目录里，
// 才能做到「上传后重启仍然在」。
const (
	brandMaxLogoBytes = 1 << 20 // 1 MiB，足够 512px 的 PNG
	brandNameMaxRunes = 40
	brandSubMaxRunes  = 60
	brandTitleMaxRune = 80
)

// 外观预设。后端只认 id，不关心具体色值——色值在前端 themes.ts 里，
// 换配色不需要动数据库；这里做白名单是为了挡住手工写入的脏值，
// 否则前端查不到对应预设会渲染成没有强调色的界面。
const (
	defaultAppearanceTheme    = "graphite"
	defaultAppearanceRadius   = "standard"
	defaultAppearanceFontSize = "standard"
	defaultAppearanceFont     = "system"
)

var appearanceThemes = map[string]bool{
	"graphite": true, // 石墨（默认青绿）
	"indigo":   true, // 靛蓝
	"azure":    true, // 湖蓝
	"pine":     true, // 松墨
	"amber":    true, // 琥珀
	"violet":   true, // 紫藤
	"rose":     true, // 胭脂
}

var appearanceRadii = map[string]bool{
	"compact":  true, // 8 / 6
	"standard": true, // 12 / 8
	"soft":     true, // 16 / 12
}

func validAppearanceTheme(v string) bool  { return appearanceThemes[v] }
func validAppearanceRadius(v string) bool { return appearanceRadii[v] }

var appearanceFontSizes = map[string]bool{
	"sm": true, "standard": true, "lg": true, "xl": true,
}

var appearanceFonts = map[string]bool{
	"system": true, // 跟随系统
	"hei":    true, // 中文无衬线
	"song":   true, // 衬线
	"mono":   true, // 等宽
}

func validAppearanceFontSize(v string) bool { return appearanceFontSizes[v] }
func validAppearanceFont(v string) bool     { return appearanceFonts[v] }

type brandConfig struct {
	AppName      string
	AppSubtitle  string
	BrowserTitle string
	LogoSource   string // builtin | upload
	LogoFile     string
	LogoVersion  string
	// 外观：配色主题、圆角档位、字号档位与字体。存的都是预设 id，
	// 具体色值与字体栈在前端，这样换一套外观不需要改数据库，
	// 也不会让旧数据出现「半个主题」。
	Theme      string
	Radius     string
	FontSize   string
	FontFamily string
	UpdatedAt  time.Time
}

// brandDir 返回自定义 logo 的存放目录。与 UPLOAD_DIR 同级，便于一起备份。
func brandDir() string {
	return env("BRAND_DIR", "./data/brand")
}

func (s *Server) ensureBrandSchema(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS brand_settings (
		id smallint PRIMARY KEY DEFAULT 1,
		app_name text NOT NULL DEFAULT '知识库管理',
		app_subtitle text NOT NULL DEFAULT '智能业务平台',
		browser_title text NOT NULL DEFAULT '知识库管理 Agent',
		logo_source text NOT NULL DEFAULT 'builtin',
		logo_file text NOT NULL DEFAULT '',
		logo_version text NOT NULL DEFAULT '',
		appearance_theme text NOT NULL DEFAULT 'graphite',
		appearance_radius text NOT NULL DEFAULT 'standard',
		appearance_font_size text NOT NULL DEFAULT 'standard',
		appearance_font text NOT NULL DEFAULT 'system',
		updated_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		logging.App().Warn("brand_schema_failed", "error", err)
		return
	}
	// 外观主题是后加的列：老库靠这两条 ALTER 补齐，不必手工跑 SQL。
	for _, stmt := range []string{
		`ALTER TABLE brand_settings ADD COLUMN IF NOT EXISTS appearance_theme text NOT NULL DEFAULT 'graphite'`,
		`ALTER TABLE brand_settings ADD COLUMN IF NOT EXISTS appearance_radius text NOT NULL DEFAULT 'standard'`,
		`ALTER TABLE brand_settings ADD COLUMN IF NOT EXISTS appearance_font_size text NOT NULL DEFAULT 'standard'`,
		`ALTER TABLE brand_settings ADD COLUMN IF NOT EXISTS appearance_font text NOT NULL DEFAULT 'system'`,
	} {
		if _, err := s.db.Exec(ctx, stmt); err != nil {
			logging.App().Warn("brand_schema_alter_failed", "error", err)
		}
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO brand_settings(id) VALUES(1) ON CONFLICT (id) DO NOTHING`); err != nil {
		logging.App().Warn("brand_seed_failed", "error", err)
	}
}

// loadBrand 读取品牌配置。读不到时返回内置默认值，
// 让界面在任何情况下都有名字可显示，而不是空白。
func (s *Server) loadBrand(ctx context.Context) brandConfig {
	def := brandConfig{
		AppName:      "知识库管理",
		AppSubtitle:  "智能业务平台",
		BrowserTitle: "知识库管理 Agent",
		LogoSource:   "builtin",
		Theme:        defaultAppearanceTheme,
		Radius:       defaultAppearanceRadius,
		FontSize:     defaultAppearanceFontSize,
		FontFamily:   defaultAppearanceFont,
	}
	if s.db == nil {
		return def
	}
	var (
		name, sub, title, source, file, version string
		theme, radius, fs, font                 string
		at                                      time.Time
	)
	err := s.db.QueryRow(ctx, `SELECT app_name, app_subtitle, browser_title, logo_source,
		logo_file, logo_version, appearance_theme, appearance_radius,
		appearance_font_size, appearance_font, updated_at
		FROM brand_settings WHERE id=1`).
		Scan(&name, &sub, &title, &source, &file, &version, &theme, &radius, &fs, &font, &at)
	if err != nil {
		return def
	}
	out := brandConfig{
		AppName:      strings.TrimSpace(name),
		AppSubtitle:  strings.TrimSpace(sub),
		BrowserTitle: strings.TrimSpace(title),
		LogoSource:   source,
		LogoFile:     file,
		LogoVersion:  version,
		Theme:        theme,
		Radius:       radius,
		FontSize:     fs,
		FontFamily:   font,
		UpdatedAt:    at,
	}
	// 数据库里可能有半截状态（例如文件被手工删了），这种时候退回内置，
	// 而不是把界面渲染成一张裂图。
	if out.LogoSource == "upload" && (out.LogoFile == "" || !fileExists(filepath.Join(brandDir(), filepath.Base(out.LogoFile)))) {
		out.LogoSource = "builtin"
	}
	if out.AppName == "" {
		out.AppName = def.AppName
	}
	// 库里可能是手工改过的脏值，认不出来就退回默认，避免前端查不到预设而渲染成无色。
	if !validAppearanceTheme(out.Theme) {
		out.Theme = defaultAppearanceTheme
	}
	if !validAppearanceRadius(out.Radius) {
		out.Radius = defaultAppearanceRadius
	}
	if !validAppearanceFontSize(out.FontSize) {
		out.FontSize = defaultAppearanceFontSize
	}
	if !validAppearanceFont(out.FontFamily) {
		out.FontFamily = defaultAppearanceFont
	}
	return out
}

// brandPayload 是给前端的形状。logo_url 带版本参数，
// 换图后 URL 变化，浏览器缓存自动失效，不需要手动清缓存。
func brandPayload(cfg brandConfig) gin.H {
	logoURL, logoType := "", ""
	if cfg.LogoSource == "upload" && cfg.LogoVersion != "" {
		logoURL = "/api/v1/brand/logo?v=" + cfg.LogoVersion
		// URL 里没有扩展名（带的是内容哈希），前端替换 favicon 时
		// 需要正确的 MIME，否则浏览器会按上一个 type 去解析。
		switch strings.ToLower(filepath.Ext(cfg.LogoFile)) {
		case ".png":
			logoType = "image/png"
		case ".jpg", ".jpeg":
			logoType = "image/jpeg"
		case ".webp":
			logoType = "image/webp"
		}
	}
	out := gin.H{
		"app_name":      cfg.AppName,
		"app_subtitle":  cfg.AppSubtitle,
		"browser_title": cfg.BrowserTitle,
		"logo_source":   cfg.LogoSource,
		"logo_url":      logoURL,
		"logo_type":     logoType,
		"theme":         cfg.Theme,
		"radius":        cfg.Radius,
		"font_size":     cfg.FontSize,
		"font_family":   cfg.FontFamily,
	}
	// updated_at 同时用字符串给前端，避免空零值 0001-01-01 在页面上出现。
	if !cfg.UpdatedAt.IsZero() {
		out["updated_at"] = cfg.UpdatedAt
	}
	return out
}

func (s *Server) brandView(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	c.JSON(http.StatusOK, brandPayload(s.loadBrand(c)))
}

type brandInput struct {
	AppName      *string `json:"app_name"`
	AppSubtitle  *string `json:"app_subtitle"`
	BrowserTitle *string `json:"browser_title"`
	LogoSource   *string `json:"logo_source"`
	Theme        *string `json:"theme"`
	Radius       *string `json:"radius"`
	FontSize     *string `json:"font_size"`
	FontFamily   *string `json:"font_family"`
}

// saveBrand 保存名称类配置。logo 的上传/恢复走单独接口，
// 因为文件与文本的提交方式不同，混在一个 handler 里会让错误定位变难。
func (s *Server) saveBrand(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in brandInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return
	}

	old := s.loadBrand(c)
	next := old

	if in.AppName != nil {
		name := strings.TrimSpace(*in.AppName)
		if name == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "项目名称不能为空"})
			return
		}
		next.AppName = truncateRunes(name, brandNameMaxRunes)
	}
	if in.AppSubtitle != nil {
		next.AppSubtitle = truncateRunes(strings.TrimSpace(*in.AppSubtitle), brandSubMaxRunes)
	}
	if in.BrowserTitle != nil {
		next.BrowserTitle = truncateRunes(strings.TrimSpace(*in.BrowserTitle), brandTitleMaxRune)
	}
	// logo_source=builtin 即「恢复默认标记」，同时把上传文件清掉。
	// 置回 upload 没有意义（没有文件），这里忽略。
	if in.Theme != nil {
		theme := strings.TrimSpace(*in.Theme)
		if !validAppearanceTheme(theme) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未知的配色主题：" + theme})
			return
		}
		next.Theme = theme
	}
	if in.Radius != nil {
		radius := strings.TrimSpace(*in.Radius)
		if !validAppearanceRadius(radius) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未知的圆角档位：" + radius})
			return
		}
		next.Radius = radius
	}
	if in.FontSize != nil {
		size := strings.TrimSpace(*in.FontSize)
		if !validAppearanceFontSize(size) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未知的字号档位：" + size})
			return
		}
		next.FontSize = size
	}
	if in.FontFamily != nil {
		font := strings.TrimSpace(*in.FontFamily)
		if !validAppearanceFont(font) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未知的字体：" + font})
			return
		}
		next.FontFamily = font
	}
	if in.LogoSource != nil && strings.TrimSpace(*in.LogoSource) == "builtin" {
		next.LogoSource = "builtin"
		next.LogoVersion = ""
	}

	if _, err := s.db.Exec(c, `UPDATE brand_settings SET
			app_name=$1, app_subtitle=$2, browser_title=$3,
			logo_source=$4, logo_version=$5,
			appearance_theme=$6, appearance_radius=$7,
			appearance_font_size=$8, appearance_font=$9, updated_at=now()
		WHERE id=1`,
		next.AppName, next.AppSubtitle, next.BrowserTitle,
		next.LogoSource, next.LogoVersion, next.Theme, next.Radius,
		next.FontSize, next.FontFamily); err != nil {
		fail(c, err)
		return
	}

	// 恢复内置时把磁盘上的自定义图删掉，避免数据目录里堆废文件。
	if next.LogoSource == "builtin" && old.LogoFile != "" {
		_ = os.Remove(filepath.Join(brandDir(), filepath.Base(old.LogoFile)))
		if _, err := s.db.Exec(c, `UPDATE brand_settings SET logo_file='' WHERE id=1`); err != nil {
			logging.App().Warn("brand_logo_clear_failed", "error", err)
		}
		next.LogoFile = ""
	}

	s.recordBrandChanges(c, old, next)
	c.JSON(http.StatusOK, brandPayload(s.loadBrand(c)))
}

// recordBrandChanges 把改动写进 setting_changes，与检索参数共用同一本账。
// logo 只记「内置 / 自定义」，不记文件名——文件名是内部实现，对读的人没有意义。
func (s *Server) recordBrandChanges(ctx context.Context, old, next brandConfig) {
	pairs := [][3]string{
		{"app_name", old.AppName, next.AppName},
		{"app_subtitle", old.AppSubtitle, next.AppSubtitle},
		{"browser_title", old.BrowserTitle, next.BrowserTitle},
		{"brand_logo", old.LogoSource, next.LogoSource},
		{"appearance_theme", old.Theme, next.Theme},
		{"appearance_radius", old.Radius, next.Radius},
		{"appearance_font_size", old.FontSize, next.FontSize},
		{"appearance_font", old.FontFamily, next.FontFamily},
	}
	for _, p := range pairs {
		if p[1] == p[2] {
			continue
		}
		if _, err := s.db.Exec(ctx, `INSERT INTO setting_changes(key,old_value,new_value,source)
			VALUES($1,$2,$3,'ui')`, p[0], p[1], p[2]); err != nil {
			logging.App().Warn("brand_change_log_failed", "key", p[0], "error", err)
		}
	}
}

// uploadBrandLogo 接收 PNG / JPEG / WebP，不接受 SVG。
//
// SVG 能内嵌 <script>，虽然作为 <img> 引用时不会执行，但这个地址是可以
// 被直接打开的；允许上传 SVG 等于开放一个同源脚本注入点。内置标记本身
// 就是 SVG，需要矢量时改代码即可。
func (s *Server) uploadBrandLogo(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, brandMaxLogoBytes+64*1024)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "没有收到文件，或文件超过 1 MB"})
		return
	}
	defer file.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	if n < 12 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件内容不完整"})
		return
	}
	head = head[:n]
	ext, ok := imageExt(head)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只接受 PNG、JPEG 或 WebP 图片"})
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		fail(c, err)
		return
	}

	dir := brandDir()
	if err := os.MkdirAll(dir, 0750); err != nil {
		fail(c, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(file, brandMaxLogoBytes+1))
	if err != nil {
		fail(c, err)
		return
	}
	if len(body) > brandMaxLogoBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图片超过 1 MB，请先压缩"})
		return
	}

	// 文件名带内容哈希：同一张图重复上传不会堆文件，换图又必然换名。
	sum := sha256.Sum256(body)
	version := hex.EncodeToString(sum[:])[:12]
	name := "logo-" + version + ext
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0640); err != nil {
		fail(c, err)
		return
	}

	old := s.loadBrand(c)
	if _, err := s.db.Exec(c, `UPDATE brand_settings SET
			logo_source='upload', logo_file=$1, logo_version=$2, updated_at=now()
		WHERE id=1`, name, version); err != nil {
		fail(c, err)
		return
	}
	if old.LogoFile != "" && old.LogoFile != name {
		_ = os.Remove(filepath.Join(dir, filepath.Base(old.LogoFile)))
	}
	if old.LogoSource != "upload" {
		if _, err := s.db.Exec(c, `INSERT INTO setting_changes(key,old_value,new_value,source)
			VALUES('brand_logo',$1,'upload','ui')`, old.LogoSource); err != nil {
			logging.App().Warn("brand_change_log_failed", "key", "brand_logo", "error", err)
		}
	}
	logging.App().Info("brand_logo_uploaded", "file", name, "bytes", len(body), "original", header.Filename)

	c.JSON(http.StatusOK, brandPayload(s.loadBrand(c)))
}

// imageExt 按魔数判断格式。只看扩展名不够——改个后缀就能把任意文件塞进来。
func imageExt(head []byte) (string, bool) {
	switch {
	case len(head) >= 8 && string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return ".png", true
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return ".jpg", true
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return ".webp", true
	}
	return "", false
}

// serveBrandLogo 直接吐文件。URL 带内容哈希，所以可以放心长期缓存。
func (s *Server) serveBrandLogo(c *gin.Context) {
	if s.db == nil {
		c.Status(http.StatusNotFound)
		return
	}
	var file, source string
	if err := s.db.QueryRow(c, `SELECT logo_file, logo_source FROM brand_settings WHERE id=1`).
		Scan(&file, &source); err != nil || source != "upload" || file == "" {
		c.Status(http.StatusNotFound)
		return
	}
	// 文件名来自数据库，仍然过一遍 Base：历史数据可能是手写的，
	// 不能让一个 ".." 把读取范围带出数据目录。
	path := filepath.Join(brandDir(), filepath.Base(file))
	if !fileExists(path) {
		c.Status(http.StatusNotFound)
		return
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		c.Header("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		c.Header("Content-Type", "image/jpeg")
	case ".webp":
		c.Header("Content-Type", "image/webp")
	default:
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.File(path)
}
