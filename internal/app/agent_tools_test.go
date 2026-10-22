package app

import (
	"strings"
	"testing"

	"knowledge-base/internal/vectorstore"
)

func TestPackVectorMatchesKeepsCitationsAligned(t *testing.T) {
	matches := []scopedMatch{
		{UnitID: "u1", UnitName: "一号项目", Match: vectorstore.Match{Filename: "a.docx", DocType: "制度", Content: strings.Repeat("档", 3000), Distance: 0.1}},
		{UnitID: "u2", UnitName: "二号项目", Match: vectorstore.Match{Filename: "b.docx", DocType: "预案", Content: strings.Repeat("案", 3000), Distance: 0.2}},
		{UnitID: "u2", UnitName: "二号项目", Match: vectorstore.Match{Filename: "c.docx", DocType: "台账", Content: "短片段", Distance: 0.3}},
	}
	items, included := packVectorMatches(matches, 12000)
	if len(items) != len(included) {
		t.Fatalf("items = %d, citations = %d", len(items), len(included))
	}
	if len(included) == 0 {
		t.Fatal("expected at least one chunk to fit")
	}
	if len(included) >= len(matches) {
		t.Fatalf("expected the budget to drop trailing chunks, included %d", len(included))
	}
	for i, match := range included {
		if items[i]["filename"] != match.Match.Filename {
			t.Fatalf("item[%d] does not correspond to included match", i)
		}
		if items[i]["unit_id"] != match.UnitID {
			t.Fatalf("item[%d] lost its unit scope", i)
		}
	}
}

func TestPackVectorMatchesIncludesEverythingUnderBudget(t *testing.T) {
	matches := []scopedMatch{
		{UnitID: "u1", UnitName: "一号项目", Match: vectorstore.Match{Filename: "a.docx", Content: "a"}},
		{UnitID: "u1", UnitName: "一号项目", Match: vectorstore.Match{Filename: "b.docx", Content: "b"}},
	}
	items, included := packVectorMatches(matches, 20000)
	if len(items) != 2 || len(included) != 2 {
		t.Fatalf("items = %d, citations = %d", len(items), len(included))
	}
}
