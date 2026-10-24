package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

const (
	maxAgentDefBytes = 1 << 20
	agentDefTimeout  = 3 * time.Second
)

var (
	agentDefIDPattern = regexp.MustCompile(`[^a-z0-9_-]+`)
	validAgentDefID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// 工具清单的唯一来源是 agent_tools.go 里的 builtinTools 声明。
// 这里只负责「按声明顺序规范化 + 校验合法性」，不再维护第二份白名单：
// 两份清单迟早会不同步，而症状是「配置了工具却不生效」，极难排查。
func allAgentToolNames() []string { return builtinToolNames() }

func normalizeAgentTools(values []string) []string {
	allowed := builtinToolSet()
	seen := make(map[string]bool, len(values))
	for _, name := range values {
		name = strings.TrimSpace(name)
		if allowed[name] {
			seen[name] = true
		}
	}
	// 按声明顺序输出，保证提示词与界面的工具清单稳定。
	ordered := make([]string, 0, len(seen))
	for _, name := range builtinToolNames() {
		if seen[name] {
			ordered = append(ordered, name)
		}
	}
	return ordered
}

func mergeExperts(base, uploaded []expert) []expert {
	out := make([]expert, 0, len(base)+len(uploaded))
	index := map[string]int{}
	for _, item := range base {
		index[item.ID] = len(out)
		out = append(out, item)
	}
	for _, item := range uploaded {
		if at, ok := index[item.ID]; ok {
			out[at] = item
			continue
		}
		index[item.ID] = len(out)
		out = append(out, item)
	}
	return out
}

func mergeSkills(base, uploaded []skill) []skill {
	out := make([]skill, 0, len(base)+len(uploaded))
	index := map[string]int{}
	for _, item := range base {
		index[item.ID] = len(out)
		out = append(out, item)
	}
	for _, item := range uploaded {
		if at, ok := index[item.ID]; ok {
			out[at] = item
			continue
		}
		index[item.ID] = len(out)
		out = append(out, item)
	}
	return out
}

func (s *Server) loadExpertDefs() ([]expert, error) {
	if s.db == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentDefTimeout)
	defer cancel()
	rows, err := s.db.Query(ctx, `SELECT id,name,description,system_prompt,tools,source FROM agent_experts ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []expert{}
	for rows.Next() {
		var item expert
		var tools []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.Description, &item.SystemPrompt, &tools, &item.Source); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(tools, &item.Tools)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Server) loadSkillDefs() ([]skill, error) {
	if s.db == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentDefTimeout)
	defer cancel()
	rows, err := s.db.Query(ctx, `SELECT id,name,description,instructions,tools,source FROM agent_skills ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []skill{}
	for rows.Next() {
		var item skill
		var tools []byte
		if err = rows.Scan(&item.ID, &item.Name, &item.Description, &item.Instructions, &tools, &item.Source); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(tools, &item.Tools)
		out = append(out, item)
	}
	return out, rows.Err()
}

// projectTools resolves the read-only tools a project run may use. Skills
// declare the tools they need; the selected agent acts as an upper bound.
// When neither side declares anything the full set is kept so older
// environment configs keep working.
func (s *Server) projectTools(expertID string, skillIDs []string) []string {
	allowed := builtinToolSet()
	wanted := map[string]bool{}
	for _, id := range skillIDs {
		wanted[strings.TrimSpace(id)] = true
	}
	skillTools := map[string]bool{}
	for _, item := range s.skillList() {
		if len(wanted) > 0 && !wanted[item.ID] {
			continue
		}
		for _, name := range item.Tools {
			if allowed[name] {
				skillTools[name] = true
			}
		}
	}
	expertTools := map[string]bool{}
	for _, item := range s.expertList() {
		if item.ID != expertID {
			continue
		}
		for _, name := range item.Tools {
			if allowed[name] {
				expertTools[name] = true
			}
		}
		break
	}

	seen := map[string]bool{}
	switch {
	case len(skillTools) == 0:
		// 未声明技能工具时沿用 Agent 的工具范围。
		for name := range expertTools {
			seen[name] = true
		}
	case len(expertTools) == 0:
		for name := range skillTools {
			seen[name] = true
		}
	default:
		for name := range skillTools {
			if expertTools[name] {
				seen[name] = true
			}
		}
		if len(seen) == 0 {
			// Agent 与技能没有交集时以技能声明为准，避免无工具可用。
			for name := range skillTools {
				seen[name] = true
			}
		}
	}

	if len(seen) == 0 {
		return allAgentToolNames()
	}
	out := make([]string, 0, len(seen))
	for _, name := range builtinToolNames() {
		if seen[name] {
			out = append(out, name)
		}
	}
	return out
}

func splitFrontMatter(raw string) (map[string]string, string) {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, strings.TrimSpace(text)
	}
	rest := text[4:]
	at := strings.Index(rest, "\n---")
	if at < 0 {
		return nil, strings.TrimSpace(text)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(rest[:at], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return fields, strings.TrimSpace(rest[at+4:])
}

func splitToolList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == ';'
	})
	return normalizeAgentTools(parts)
}

func defIDFromFilename(filename string) string {
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename)))
	base = agentDefIDPattern.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = fmt.Sprintf("def-%d", time.Now().UnixNano())
	}
	if len(base) > 64 {
		base = base[:64]
	}
	return base
}

func readAgentDefUpload(c *gin.Context) (string, []byte, error) {
	file, err := c.FormFile("file")
	if err != nil {
		return "", nil, errors.New("请选择要上传的 JSON 或 Markdown 定义文件")
	}
	if file.Size > maxAgentDefBytes {
		return "", nil, fmt.Errorf("定义文件不能超过 %d KB", maxAgentDefBytes/1024)
	}
	src, err := file.Open()
	if err != nil {
		return "", nil, err
	}
	defer src.Close()
	raw, err := io.ReadAll(io.LimitReader(src, maxAgentDefBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(raw) > maxAgentDefBytes {
		return "", nil, fmt.Errorf("定义文件不能超过 %d KB", maxAgentDefBytes/1024)
	}
	return file.Filename, raw, nil
}

func parseSkillDef(filename string, raw []byte) (skill, error) {
	text := strings.TrimSpace(string(raw))
	item := skill{Source: "upload"}
	if strings.HasSuffix(strings.ToLower(filename), ".json") || strings.HasPrefix(text, "{") {
		var payload struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			Description  string   `json:"description"`
			Instructions string   `json:"instructions"`
			Tools        []string `json:"tools"`
		}
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return item, fmt.Errorf("JSON 解析失败：%w", err)
		}
		item.ID, item.Name, item.Description = payload.ID, payload.Name, payload.Description
		item.Instructions, item.Tools = payload.Instructions, normalizeAgentTools(payload.Tools)
	} else {
		fields, body := splitFrontMatter(text)
		item.ID = fields["id"]
		item.Name = fields["name"]
		item.Description = fields["description"]
		item.Instructions = body
		item.Tools = splitToolList(fields["tools"])
	}
	if item.ID == "" {
		item.ID = defIDFromFilename(filename)
	}
	if item.Name == "" {
		item.Name = item.ID
	}
	if item.Instructions == "" {
		return item, errors.New("技能定义缺少 instructions（JSON 字段或 Markdown 正文）")
	}
	if !validAgentDefID.MatchString(item.ID) {
		return item, errors.New("技能 id 只能包含字母、数字、下划线和短横线，且以字母或数字开头")
	}
	return item, nil
}

func parseExpertDef(filename string, raw []byte) (expert, error) {
	text := strings.TrimSpace(string(raw))
	item := expert{Source: "upload"}
	if strings.HasSuffix(strings.ToLower(filename), ".json") || strings.HasPrefix(text, "{") {
		var payload struct {
			ID           string   `json:"id"`
			Name         string   `json:"name"`
			Description  string   `json:"description"`
			SystemPrompt string   `json:"system_prompt"`
			Tools        []string `json:"tools"`
		}
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			return item, fmt.Errorf("JSON 解析失败：%w", err)
		}
		item.ID, item.Name, item.Description = payload.ID, payload.Name, payload.Description
		item.SystemPrompt, item.Tools = payload.SystemPrompt, normalizeAgentTools(payload.Tools)
	} else {
		fields, body := splitFrontMatter(text)
		item.ID = fields["id"]
		item.Name = fields["name"]
		item.Description = fields["description"]
		item.SystemPrompt = body
		item.Tools = splitToolList(fields["tools"])
	}
	if item.ID == "" {
		item.ID = defIDFromFilename(filename)
	}
	if item.Name == "" {
		item.Name = item.ID
	}
	if item.SystemPrompt == "" {
		return item, errors.New("Agent 定义缺少 system_prompt（JSON 字段或 Markdown 正文）")
	}
	if !validAgentDefID.MatchString(item.ID) {
		return item, errors.New("Agent id 只能包含字母、数字、下划线和短横线，且以字母或数字开头")
	}
	return item, nil
}

func (s *Server) saveSkillDef(ctx context.Context, item skill) error {
	tools, _ := json.Marshal(item.Tools)
	toolsJSON := json.RawMessage(tools)
	_, err := s.db.Exec(ctx, `INSERT INTO agent_skills(id,name,description,instructions,tools,source,updated_at)
		VALUES($1,$2,$3,$4,$5,'upload',now())
		ON CONFLICT (id) DO UPDATE SET name=excluded.name,description=excluded.description,
		instructions=excluded.instructions,tools=excluded.tools,source='upload',updated_at=now()`,
		item.ID, item.Name, item.Description, item.Instructions, toolsJSON)
	return err
}

func (s *Server) saveExpertDef(ctx context.Context, item expert) error {
	tools, _ := json.Marshal(item.Tools)
	toolsJSON := json.RawMessage(tools)
	_, err := s.db.Exec(ctx, `INSERT INTO agent_experts(id,name,description,system_prompt,tools,source,updated_at)
		VALUES($1,$2,$3,$4,$5,'upload',now())
		ON CONFLICT (id) DO UPDATE SET name=excluded.name,description=excluded.description,
		system_prompt=excluded.system_prompt,tools=excluded.tools,source='upload',updated_at=now()`,
		item.ID, item.Name, item.Description, item.SystemPrompt, toolsJSON)
	return err
}

func (s *Server) agentDefinitions(c *gin.Context) {
	c.JSON(200, gin.H{"experts": s.expertList(), "skills": s.skillList()})
}

func (s *Server) uploadSkillDef(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	filename, raw, err := readAgentDefUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := parseSkillDef(filename, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if id := strings.TrimSpace(c.PostForm("id")); id != "" {
		item.ID = id
	}
	if err = s.saveSkillDef(c.Request.Context(), item); err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("agent_skill_uploaded", "request_id", c.GetString(requestIDKey), "id", item.ID, "file", filename)
	c.JSON(http.StatusCreated, item)
}

func (s *Server) uploadExpertDef(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	filename, raw, err := readAgentDefUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := parseExpertDef(filename, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if id := strings.TrimSpace(c.PostForm("id")); id != "" {
		item.ID = id
	}
	if err = s.saveExpertDef(c.Request.Context(), item); err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("agent_expert_uploaded", "request_id", c.GetString(requestIDKey), "id", item.ID, "file", filename)
	c.JSON(http.StatusCreated, item)
}

func (s *Server) deleteSkillDef(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	tag, err := s.db.Exec(c, `DELETE FROM agent_skills WHERE id=$1`, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "skill not found"})
		return
	}
	c.JSON(200, gin.H{"deleted": c.Param("id")})
}

func (s *Server) deleteExpertDef(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	tag, err := s.db.Exec(c, `DELETE FROM agent_experts WHERE id=$1`, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "expert not found"})
		return
	}
	c.JSON(200, gin.H{"deleted": c.Param("id")})
}
