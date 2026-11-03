package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

// 远程导入把语雀 / GitHub 的内容抓成文件后走同一条入库管线：
// 落盘 → index_jobs(queued) → worker 解析索引。
// 因此导入的文档与手工上传完全同权：去重、重试、共享、删除都一致。

const (
	importMaxFiles      = 200
	importMaxFileBytes  = 20 << 20
	importConcurrency   = 4
	importRequestBudget = 60 * time.Second
)

var unsafeFilename = regexp.MustCompile(`[\\/:*?"<>|\s]+`)

type importRequest struct {
	UnitID  string `json:"unit_id"`
	DocType string `json:"doc_type"`
	Limit   int    `json:"limit"`
	// 语雀：Token + namespace/slug
	Token string `json:"token"`
	Repo  string `json:"repo"`
	// GitHub：owner/repo + 分支 + 子目录
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Ref   string `json:"ref"`
	Path  string `json:"path"`
}

type importFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type importResult struct {
	Queued  int             `json:"queued"`
	Skipped int             `json:"skipped"`
	Failed  []importFailure `json:"failed"`
}

// importItem 是一个待入库的远端文档。
type importItem struct {
	Name    string
	Content []byte
	// RemoteID / Revision 用于增量同步：版本未变化就不再下载。
	RemoteID string
	Revision string
	// 远端标识，用于日志与失败定位。
	Source string
}

// enqueueDocumentBytes 把一段内容落盘并创建索引任务，与上传接口共用同一套队列。
func (s *Server) enqueueDocumentBytes(unit, docType, filename string, content []byte) (*Job, error) {
	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	dir := filepath.Join(env("UPLOAD_DIR", "./data/uploads"), filepath.Base(unit))
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, jobID+"_"+filename)
	if err := os.WriteFile(dst, content, 0640); err != nil {
		return nil, err
	}
	j := &Job{ID: jobID, UnitID: unit, DocType: docType, Filename: filename, Path: dst,
		Status: "queued", Phase: phasePending, Message: "等待解析"}
	if err := s.persistJob(j); err != nil {
		_ = os.Remove(dst)
		return nil, err
	}
	s.jobs.Store(jobID, j)
	return j, nil
}

// runImport 并发抓取并入库，返回汇总结果。单个文件失败不影响其它文件。
func (s *Server) runImport(ctx context.Context, unit, docType string, items []importItem) importResult {
	result := importResult{Failed: []importFailure{}}
	sem := make(chan struct{}, importConcurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup

	seen := map[string]struct{}{}
	for _, item := range items {
		name := sanitizeImportName(item.Name)
		if name == "" {
			mu.Lock()
			result.Skipped++
			mu.Unlock()
			continue
		}
		mu.Lock()
		if _, dup := seen[name]; dup {
			result.Skipped++
			mu.Unlock()
			continue
		}
		seen[name] = struct{}{}
		mu.Unlock()

		if len(item.Content) == 0 {
			mu.Lock()
			result.Failed = append(result.Failed, importFailure{Name: name, Error: "内容为空"})
			mu.Unlock()
			continue
		}
		if len(item.Content) > importMaxFileBytes {
			mu.Lock()
			result.Failed = append(result.Failed, importFailure{Name: name, Error: "超过 20MB 上限"})
			mu.Unlock()
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(item importItem, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := s.enqueueDocumentBytes(unit, docType, name, item.Content); err != nil {
				mu.Lock()
				result.Failed = append(result.Failed, importFailure{Name: name, Error: err.Error()})
				mu.Unlock()
				return
			}
			mu.Lock()
			result.Queued++
			mu.Unlock()
			logging.App().Info("remote_import_queued", "unit_id", unit, "filename", name, "source", item.Source)
		}(item, name)
	}
	wg.Wait()
	if result.Queued > 0 {
		s.wakeWorker()
	}
	return result
}

// sanitizeImportName 只保留安全字符，避免远端 title 里的 / 或引号变成路径。
func sanitizeImportName(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return ""
	}
	name = unsafeFilename.ReplaceAllString(name, "_")
	if !strings.HasSuffix(strings.ToLower(name), ".md") &&
		!strings.HasSuffix(strings.ToLower(name), ".markdown") &&
		!strings.HasSuffix(strings.ToLower(name), ".txt") &&
		!strings.HasSuffix(strings.ToLower(name), ".html") &&
		!strings.HasSuffix(strings.ToLower(name), ".htm") {
		name += ".md"
	}
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120]) + filepath.Ext(name)
	}
	return name
}

// importYuque 拉取语雀知识库下的全部文档。
// 语雀 API：GET /api/v2/repos/{namespace}/{slug}/docs 列出文档，
// GET /api/v2/repos/{namespace}/{slug}/docs/{id} 取正文（format=markdown 时 body 即 Markdown）。
func (s *Server) importYuque(c *gin.Context) {
	var in importRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	unit, err := s.importUnit(c, in.UnitID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	token := strings.TrimSpace(in.Token)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写语雀 Token"})
		return
	}
	repo := strings.Trim(strings.TrimSpace(in.Repo), "/")
	if parts := strings.Split(repo, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "知识库格式应为 namespace/仓库名"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), importRequestBudget)
	defer cancel()

	src := importSource{UnitID: unit, Kind: "yuque",
		Config: importSourceConfig{Repo: repo, DocType: in.DocType, Limit: in.Limit}}
	items, _, _, err := s.collectImportItems(ctx, src, token, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusOK, gin.H{"queued": 0, "skipped": 0, "failed": []any{}, "message": "该知识库下没有可导入的文档"})
		return
	}

	result := s.runImport(ctx, unit, importDocType(in.DocType), items)
	logging.App().Info("yuque_import_completed",
		"unit_id", unit, "repo", repo, "queued", result.Queued, "skipped", result.Skipped, "failed", len(result.Failed))
	c.JSON(http.StatusOK, result)
}

// yuqueTOCNode 是语雀目录树的一项：TITLE 为分组，DOC 为文档。
type yuqueTOCNode struct {
	UUID       string `json:"uuid"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	DocID      int    `json:"doc_id"`
	ParentUUID string `json:"parent_uuid"`
}

// fetchYuqueTOC 把目录树压平成「doc_id → 分组路径」。
// 语雀的分组可以多层嵌套，这里按 parent_uuid 逐级向上拼，最多 8 层防环。
func fetchYuqueTOC(ctx context.Context, token, repo string) (map[int]string, error) {
	body, err := yuqueGet(ctx, token, "https://www.yuque.com/api/v2/repos/"+repo+"/toc")
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []yuqueTOCNode `json:"data"`
	}
	if err = json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	byUUID := make(map[string]yuqueTOCNode, len(parsed.Data))
	for _, node := range parsed.Data {
		byUUID[node.UUID] = node
	}

	out := map[int]string{}
	for _, node := range parsed.Data {
		if node.DocID == 0 {
			continue
		}
		parts := []string{}
		current := node
		for depth := 0; depth < 8; depth++ {
			if current.ParentUUID == "" {
				break
			}
			parent, ok := byUUID[current.ParentUUID]
			if !ok {
				break
			}
			if title := strings.TrimSpace(parent.Title); title != "" {
				parts = append([]string{title}, parts...)
			}
			current = parent
		}
		if len(parts) > 0 {
			out[node.DocID] = strings.Join(parts, "/")
		}
	}
	return out, nil
}

type yuqueDoc struct {
	ID        int    `json:"id"`
	Slug      string `json:"slug"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

func fetchYuqueDocList(ctx context.Context, token, repo string, limit int) ([]yuqueDoc, error) {
	out := []yuqueDoc{}
	for offset := 0; len(out) < limit; offset += 100 {
		endpoint := fmt.Sprintf("https://www.yuque.com/api/v2/repos/%s/docs?limit=100&offset=%d", repo, offset)
		body, err := yuqueGet(ctx, token, endpoint)
		if err != nil {
			return nil, err
		}
		var parsed struct {
			Data []yuqueDoc `json:"data"`
		}
		if err = json.Unmarshal(body, &parsed); err != nil {
			return nil, err
		}
		if len(parsed.Data) == 0 {
			break
		}
		out = append(out, parsed.Data...)
		if len(parsed.Data) < 100 {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fetchYuqueDoc 返回正文与格式。语雀对 markdown 文档返回 body，
// 老文档可能是 lake/html，统一按 HTML 存盘交给现有解析器。
func fetchYuqueDoc(ctx context.Context, token, repo string, id int) ([]byte, string, error) {
	endpoint := fmt.Sprintf("https://www.yuque.com/api/v2/repos/%s/docs/%d", repo, id)
	body, err := yuqueGet(ctx, token, endpoint)
	if err != nil {
		return nil, "", err
	}
	var parsed struct {
		Data struct {
			Format   string `json:"format"`
			Body     string `json:"body"`
			BodyHTML string `json:"body_html"`
			BodyLake string `json:"body_lake"`
		} `json:"data"`
	}
	if err = json.Unmarshal(body, &parsed); err != nil {
		return nil, "", err
	}
	format := parsed.Data.Format
	if strings.TrimSpace(parsed.Data.Body) != "" && (format == "" || format == "markdown") {
		return []byte(parsed.Data.Body), "markdown", nil
	}
	if strings.TrimSpace(parsed.Data.BodyHTML) != "" {
		return []byte(parsed.Data.BodyHTML), "html", nil
	}
	if strings.TrimSpace(parsed.Data.BodyLake) != "" {
		return []byte(parsed.Data.BodyLake), "lake", nil
	}
	return []byte(parsed.Data.Body), format, nil
}

func yuqueGet(ctx context.Context, token, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "yinju-importer")
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, importMaxFileBytes))
	if res.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("Token 无效或已过期（401）")
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, errors.New("知识库不存在或无权访问（404）")
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, truncateRunes(string(body), 120))
	}
	return body, nil
}

// importGithub 拉取仓库指定目录下的文本类文件。
func (s *Server) importGithub(c *gin.Context) {
	var in importRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体不是合法 JSON"})
		return
	}
	unit, err := s.importUnit(c, in.UnitID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	owner := strings.TrimSpace(in.Owner)
	name := strings.TrimSpace(in.Name)
	if owner == "" || name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写 owner 与仓库名"})
		return
	}
	ref := strings.TrimSpace(in.Ref)
	if ref == "" {
		ref = "HEAD"
	}
	subPath := strings.Trim(strings.TrimSpace(in.Path), "/")

	ctx, cancel := context.WithTimeout(c.Request.Context(), importRequestBudget)
	defer cancel()

	src := importSource{UnitID: unit, Kind: "github",
		Config: importSourceConfig{Owner: owner, Name: name, Ref: ref, Path: subPath, DocType: in.DocType, Limit: in.Limit}}
	items, _, _, err := s.collectImportItems(ctx, src, in.Token, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if len(items) == 0 {
		c.JSON(http.StatusOK, gin.H{"queued": 0, "skipped": 0, "failed": []any{}, "message": "该目录下没有可导入的文本文件"})
		return
	}

	result := s.runImport(ctx, unit, importDocType(in.DocType), items)
	logging.App().Info("github_import_completed",
		"unit_id", unit, "repo", owner+"/"+name, "queued", result.Queued, "skipped", result.Skipped, "failed", len(result.Failed))
	c.JSON(http.StatusOK, result)
}

var importableExt = map[string]bool{
	".md": true, ".markdown": true, ".mdx": true, ".txt": true,
	".html": true, ".htm": true, ".csv": true, ".json": true,
}

// githubFile 是仓库里的一个文本文件，SHA 用于判断内容是否变化。
type githubFile struct {
	Path string
	SHA  string
}

// listGithubFiles 用 git tree API 一次拿到全量路径，按扩展名与子目录过滤。
func listGithubFiles(ctx context.Context, token, owner, repo, ref, subPath string, limit int) ([]githubFile, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", githubAPIBase(), owner, repo, url.PathEscape(ref))
	body, err := githubGet(ctx, token, endpoint)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err = json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := []githubFile{}
	for _, node := range parsed.Tree {
		if node.Type != "blob" {
			continue
		}
		if subPath != "" && !strings.HasPrefix(node.Path, subPath+"/") {
			continue
		}
		if !importableExt[strings.ToLower(filepath.Ext(node.Path))] {
			continue
		}
		out = append(out, githubFile{Path: node.Path, SHA: node.SHA})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func downloadGithubFile(ctx context.Context, token, owner, repo, ref, path string) ([]byte, error) {
	// 用 contents API 而不是 raw：私有仓库同样可用，且能拿到 base64 内容。
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		githubAPIBase(), owner, repo, url.PathEscape(path), url.QueryEscape(ref))
	body, err := githubGet(ctx, token, endpoint)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	if err = json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Size > importMaxFileBytes {
		return nil, errors.New("文件超过 20MB 上限")
	}
	if parsed.Encoding != "base64" {
		return nil, errors.New("不支持的编码：" + parsed.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(parsed.Content, "\n", ""))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func githubGet(ctx context.Context, token, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "yinju-importer")
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		// 国内网络常无法直连 api.github.com：提示可配置代理或镜像地址。
		return nil, fmt.Errorf("无法连接 %s（如网络受限可设置 HTTPS_PROXY 或 GITHUB_API_BASE）：%w", githubAPIBase(), err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, importMaxFileBytes))
	switch res.StatusCode {
	case http.StatusNotFound:
		return nil, errors.New("仓库或路径不存在（404）；私有仓库需要 Token")
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("认证失败或触发限流（%d）", res.StatusCode)
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, truncateRunes(string(body), 120))
	}
	return body, nil
}

// githubAPIBase 允许指向 GitHub Enterprise 或自建镜像；默认官方 API。
func githubAPIBase() string {
	if base := strings.TrimRight(strings.TrimSpace(env("GITHUB_API_BASE", "")), "/"); base != "" {
		return base
	}
	return "https://api.github.com"
}

func (s *Server) importUnit(c *gin.Context, unitID string) (string, error) {
	id := strings.TrimSpace(unitID)
	if id == "" {
		return "", errors.New("请选择知识库")
	}
	exists, err := s.unitExists(c, id)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errUnitNotFound
	}
	return id, nil
}

func importLimit(v int) int {
	if v < 1 || v > importMaxFiles {
		return 50
	}
	return v
}

func importDocType(v string) string {
	if t := strings.TrimSpace(v); t != "" {
		return t
	}
	return "导入"
}
