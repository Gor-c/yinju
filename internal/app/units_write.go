package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

const maxUnitDescRunes = 60

type unitInput struct {
	UnitID    string   `json:"unit_id"`
	Name      string   `json:"name"`
	UnitType  string   `json:"unit_type"`
	Industry  string   `json:"industry"`
	ExpertID  string   `json:"expert_id"`
	SkillIDs  []string `json:"skill_ids"`
	ToolNames []string `json:"tool_names"`
	Tags      []string `json:"tags"`
}

type unitPatch struct {
	Name      *string   `json:"name"`
	UnitType  *string   `json:"unit_type"`
	Industry  *string   `json:"industry"`
	ExpertID  *string   `json:"expert_id"`
	SkillIDs  *[]string `json:"skill_ids"`
	ToolNames *[]string `json:"tool_names"`
	Tags      *[]string `json:"tags"`
}

type unitAgentConfig struct {
	UnitID    string   `json:"unit_id"`
	ExpertID  string   `json:"expert_id"`
	SkillIDs  []string `json:"skill_ids"`
	ToolNames []string `json:"tool_names"`
	Tools     []string `json:"tools"`
}

func cleanSkillIDs(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, id := range values {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// 返回 json.RawMessage，pgx 会按 JSON 编码写入 jsonb 列；[]byte 会被当成 bytea。
func encodeSkillIDs(values []string) json.RawMessage {
	if values == nil {
		values = []string{}
	}
	raw, _ := json.Marshal(values)
	return json.RawMessage(raw)
}

func decodeSkillIDs(raw []byte) []string {
	out := []string{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func cleanTags(values []string) []string {
	out := cleanSkillIDs(values)
	if len(out) > 20 {
		out = out[:20]
	}
	for i, tag := range out {
		out[i] = truncateRunes(tag, 20)
	}
	return out
}

func validateUnitName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("项目名称不能为空")
	}
	if len([]rune(name)) > maxUnitNameRunes {
		return fmt.Errorf("项目名称不能超过 %d 个字符", maxUnitNameRunes)
	}
	return nil
}

func validateUnitLabel(value, label string) error {
	if len([]rune(value)) > maxUnitDescRunes {
		return fmt.Errorf("%s不能超过 %d 个字符", label, maxUnitDescRunes)
	}
	return nil
}

func (s *Server) createUnit(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in unitInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := validateUnitName(in.Name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateUnitLabel(in.UnitType, "项目分类"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateUnitLabel(in.Industry, "行业"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id := strings.TrimSpace(in.UnitID)
	if id == "" {
		id = newUnitID()
	}
	skills := cleanSkillIDs(in.SkillIDs)
	tools := normalizeAgentTools(in.ToolNames)
	tags := cleanTags(in.Tags)
	_, err := s.db.Exec(c, `INSERT INTO units(unit_id,name,unit_type,industry,expert_id,skill_ids,tool_names,tags)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, in.Name, strings.TrimSpace(in.UnitType), strings.TrimSpace(in.Industry), strings.TrimSpace(in.ExpertID), encodeSkillIDs(skills), encodeSkillIDs(tools), encodeSkillIDs(tags))
	if err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("unit_created", "request_id", c.GetString(requestIDKey), "unit_id", id, "name", in.Name, "source", "api")
	c.JSON(http.StatusCreated, gin.H{
		"unit_id":    id,
		"name":       in.Name,
		"unit_type":  strings.TrimSpace(in.UnitType),
		"industry":   strings.TrimSpace(in.Industry),
		"expert_id":  strings.TrimSpace(in.ExpertID),
		"skill_ids":  skills,
		"tool_names": tools,
		"tags":       tags,
	})
}

func (s *Server) updateUnit(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	var in unitPatch
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	id := c.Param("id")
	var (
		name, unitType, industry, expertID string
		skillRaw, toolRaw, tagRaw          []byte
	)
	err := s.db.QueryRow(c, `SELECT name,unit_type,industry,expert_id,skill_ids,tool_names,tags FROM units WHERE unit_id=$1`, id).
		Scan(&name, &unitType, &industry, &expertID, &skillRaw, &toolRaw, &tagRaw)
	if err != nil {
		fail(c, err)
		return
	}
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
		if err = validateUnitName(name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if in.UnitType != nil {
		unitType = strings.TrimSpace(*in.UnitType)
		if err = validateUnitLabel(unitType, "项目分类"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if in.Industry != nil {
		industry = strings.TrimSpace(*in.Industry)
		if err = validateUnitLabel(industry, "行业"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if in.ExpertID != nil {
		expertID = strings.TrimSpace(*in.ExpertID)
	}
	skills := decodeSkillIDs(skillRaw)
	if in.SkillIDs != nil {
		skills = cleanSkillIDs(*in.SkillIDs)
	}
	tools := decodeSkillIDs(toolRaw)
	if in.ToolNames != nil {
		tools = normalizeAgentTools(*in.ToolNames)
	}
	tags := decodeSkillIDs(tagRaw)
	if in.Tags != nil {
		tags = cleanTags(*in.Tags)
	}
	_, err = s.db.Exec(c, `UPDATE units SET name=$2,unit_type=$3,industry=$4,expert_id=$5,skill_ids=$6,tool_names=$7,tags=$8 WHERE unit_id=$1`,
		id, name, unitType, industry, expertID, encodeSkillIDs(skills), encodeSkillIDs(tools), encodeSkillIDs(tags))
	if err != nil {
		fail(c, err)
		return
	}
	logging.App().Info("unit_updated", "request_id", c.GetString(requestIDKey), "unit_id", id)
	c.JSON(200, gin.H{
		"unit_id":    id,
		"name":       name,
		"unit_type":  unitType,
		"industry":   industry,
		"expert_id":  expertID,
		"skill_ids":  skills,
		"tool_names": tools,
		"tags":       tags,
	})
}

// effectiveTools prefers tools declared directly on a project and falls back
// to the agent/skill derivation when nothing is declared.
func (s *Server) effectiveTools(expertID string, skillIDs, declared []string) []string {
	if tools := normalizeAgentTools(declared); len(tools) > 0 {
		return tools
	}
	return s.projectTools(expertID, skillIDs)
}

// unitAgentConfig resolves the agent, skills and read-only tools a project
// brings into the chat page.
func (s *Server) unitAgentConfig(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id := c.Param("id")
	var expertID string
	var skillRaw, toolRaw []byte
	err := s.db.QueryRow(c, `SELECT expert_id,skill_ids,tool_names FROM units WHERE unit_id=$1`, id).
		Scan(&expertID, &skillRaw, &toolRaw)
	if err != nil {
		fail(c, err)
		return
	}
	skills := decodeSkillIDs(skillRaw)
	tools := decodeSkillIDs(toolRaw)
	c.JSON(200, unitAgentConfig{
		UnitID:    id,
		ExpertID:  expertID,
		SkillIDs:  skills,
		ToolNames: tools,
		Tools:     s.effectiveTools(expertID, skills, tools),
	})
}

// unitAgentDefaults returns the agent, skills and tools configured on a
// project so a chat request that omits them still picks up the defaults.
func (s *Server) unitAgentDefaults(ctx context.Context, unitID string) (string, []string, []string, error) {
	var expertID string
	var skillRaw, toolRaw []byte
	err := s.db.QueryRow(ctx, `SELECT expert_id,skill_ids,tool_names FROM units WHERE unit_id=$1`, unitID).
		Scan(&expertID, &skillRaw, &toolRaw)
	if err != nil {
		return "", nil, nil, err
	}
	return expertID, decodeSkillIDs(skillRaw), decodeSkillIDs(toolRaw), nil
}

// unitProfiles returns profiles for several projects at once so the chat page
// can render the selected knowledge bases in one request.
func (s *Server) unitProfiles(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	ids := cleanSkillIDs(strings.Split(c.Query("ids"), ","))
	if len(ids) == 0 {
		c.JSON(200, []gin.H{})
		return
	}
	out, err := s.unitProfilesScoped(c, ids)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, out)
}

// unitMeta returns the distinct categories and industries already in use so
// the UI can offer them as suggestions instead of a hard-coded list.
func (s *Server) unitMeta(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	collect := func(column string) ([]string, error) {
		rows, err := s.db.Query(c, `SELECT DISTINCT `+column+` FROM units WHERE `+column+`<>'' ORDER BY 1`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, rows.Err()
	}
	categories, err := collect("unit_type")
	if err != nil {
		fail(c, err)
		return
	}
	industries, err := collect("industry")
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"categories": categories, "industries": industries})
}
