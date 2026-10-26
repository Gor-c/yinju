package app

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDocumentExportAgentDefinitionIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../agents/document-export.json")
	if err != nil {
		t.Fatal(err)
	}
	item, err := parseExpertDef("document-export.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "document-export" || item.Name != "文档导出助手" {
		t.Fatalf("unexpected definition: %#v", item)
	}
	if !reflect.DeepEqual(item.Tools, []string{
		"get_unit_profile", "query_hazards", "audit_history", "vector_search",
	}) {
		t.Fatalf("tools = %#v", item.Tools)
	}
	if !strings.Contains(item.SystemPrompt, "Markdown") || !strings.Contains(item.SystemPrompt, "CSV") {
		t.Fatalf("prompt does not describe supported export formats")
	}
}

func TestParseSkillDefMarkdown(t *testing.T) {
	raw := []byte("---\nid: risk-review\nname: 风险复核\ndescription: 复核隐患与审核\ntools: query_hazards, audit_history\n---\n\n先看隐患，再核对审核记录。\n")
	item, err := parseSkillDef("risk.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "risk-review" || item.Name != "风险复核" {
		t.Fatalf("unexpected definition: %#v", item)
	}
	if item.Instructions != "先看隐患，再核对审核记录。" {
		t.Fatalf("instructions = %q", item.Instructions)
	}
	if !reflect.DeepEqual(item.Tools, []string{"query_hazards", "audit_history"}) {
		t.Fatalf("tools = %#v", item.Tools)
	}
}

func TestParseSkillDefJSONFiltersUnknownTools(t *testing.T) {
	raw := []byte(`{"id":"kb-extra","name":"扩展","instructions":"按档案回答","tools":["vector_search","drop_table"]}`)
	item, err := parseSkillDef("skill.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(item.Tools, []string{"vector_search"}) {
		t.Fatalf("tools = %#v", item.Tools)
	}
	if item.Source != "upload" {
		t.Fatalf("source = %q", item.Source)
	}
}

func TestParseExpertDefRequiresPrompt(t *testing.T) {
	if _, err := parseExpertDef("agent.json", []byte(`{"id":"empty"}`)); err == nil {
		t.Fatal("expected error for missing system_prompt")
	}
	item, err := parseExpertDef("agent.json", []byte(`{"id":"reviewer","name":"审核员","system_prompt":"谨慎回答","tools":["audit_history"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "reviewer" || len(item.Tools) != 1 {
		t.Fatalf("unexpected definition: %#v", item)
	}
}

func TestParseSkillDefRejectsInvalidID(t *testing.T) {
	if _, err := parseSkillDef("x.json", []byte(`{"id":"bad id","name":"x","instructions":"y"}`)); err == nil {
		t.Fatal("expected invalid id error")
	}
}

func TestProjectToolsFollowsSelectedSkills(t *testing.T) {
	s := testServer()
	if got := s.projectTools("kb-expert", []string{"kb-search"}); !reflect.DeepEqual(got, []string{"vector_search"}) {
		t.Fatalf("search only = %#v", got)
	}
	if got := s.projectTools("kb-expert", []string{"kb-review"}); !reflect.DeepEqual(got, []string{"query_hazards", "audit_history"}) {
		t.Fatalf("review only = %#v", got)
	}
	if got := s.projectTools("kb-expert", []string{"kb-search", "kb-review"}); len(got) != 3 {
		t.Fatalf("combined = %#v", got)
	}
	if got := s.projectTools("unknown-expert", []string{"kb-search"}); !reflect.DeepEqual(got, []string{"vector_search"}) {
		t.Fatalf("unknown expert = %#v", got)
	}
}

func TestEffectiveToolsPrefersDeclared(t *testing.T) {
	s := testServer()
	if got := s.effectiveTools("kb-expert", []string{"kb-search"}, []string{"query_hazards"}); !reflect.DeepEqual(got, []string{"query_hazards"}) {
		t.Fatalf("declared tools = %#v", got)
	}
	if got := s.effectiveTools("kb-expert", []string{"kb-search"}, nil); !reflect.DeepEqual(got, []string{"vector_search"}) {
		t.Fatalf("derived tools = %#v", got)
	}
	// 非法或空白声明应回退到推导结果。
	if got := s.effectiveTools("kb-expert", []string{"kb-search"}, []string{"not_a_tool"}); !reflect.DeepEqual(got, []string{"vector_search"}) {
		t.Fatalf("fallback tools = %#v", got)
	}
}

func TestNormalizeAgentToolsKeepsCanonicalOrder(t *testing.T) {
	got := normalizeAgentTools([]string{"vector_search", "bogus", "get_unit_profile", "vector_search"})
	if !reflect.DeepEqual(got, []string{"get_unit_profile", "vector_search"}) {
		t.Fatalf("tools = %#v", got)
	}
}

func TestSplitFrontMatterWithoutHeader(t *testing.T) {
	fields, body := splitFrontMatter("  纯文本技能说明  ")
	if fields != nil {
		t.Fatalf("fields = %#v", fields)
	}
	if !strings.Contains(body, "纯文本技能说明") {
		t.Fatalf("body = %q", body)
	}
}
