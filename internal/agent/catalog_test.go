package agent

import (
	"context"
	"encoding/json"
	"testing"
)

type stubTool struct{ name string }

func (s stubTool) Definition() ToolDef { return ToolDef{Name: s.name} }

func (s stubTool) Execute(context.Context, json.RawMessage) (any, error) { return nil, nil }

func newTestCatalog() *Catalog {
	catalog := NewCatalog()
	catalog.Add(ToolSpec{Name: "alpha", Namespace: "test"}, stubTool{name: "alpha"})
	catalog.Add(ToolSpec{Name: "beta", Namespace: "test"}, stubTool{name: "beta"})
	catalog.Add(ToolSpec{Name: "gamma", Namespace: "test"}, stubTool{name: "gamma"})
	catalog.AddAlways(ToolSpec{Name: "always", Namespace: "runtime"}, stubTool{name: "always"})
	return catalog
}

func TestCatalogRegistryFiltersByName(t *testing.T) {
	registry := newTestCatalog().Registry([]string{"beta"})
	names := registry.Names()
	// beta 被选中；always 类工具不受白名单裁剪。
	if len(names) != 2 {
		t.Fatalf("expected beta + always, got %v", names)
	}
	if _, ok := registry.Get("beta"); !ok {
		t.Fatalf("beta should be present: %v", names)
	}
	if _, ok := registry.Get("always"); !ok {
		t.Fatalf("always-on tool must survive filtering: %v", names)
	}
	if _, ok := registry.Get("alpha"); ok {
		t.Fatalf("alpha should have been filtered out: %v", names)
	}
}

func TestCatalogRegistryEmptyNamesKeepsEverything(t *testing.T) {
	registry := newTestCatalog().Registry(nil)
	if len(registry.Names()) != 4 {
		t.Fatalf("empty selection should keep all tools, got %v", registry.Names())
	}
}

func TestCatalogSelectKeepsDeclarationOrder(t *testing.T) {
	// 用户声明的顺序是乱的，且包含未知工具与重复项。
	got := newTestCatalog().Select([]string{"gamma", "unknown", "alpha", "gamma"})
	want := []string{"alpha", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCatalogSpecsExposeMetadata(t *testing.T) {
	specs := newTestCatalog().Specs()
	if len(specs) != 4 {
		t.Fatalf("expected 4 specs, got %d", len(specs))
	}
	if specs[0].Name != "alpha" || specs[0].Namespace != "test" {
		t.Fatalf("spec metadata lost: %+v", specs[0])
	}
}

func TestCatalogIgnoresEmptyRegistration(t *testing.T) {
	catalog := NewCatalog()
	catalog.Add(ToolSpec{Name: ""}, stubTool{name: "nameless"})
	catalog.Add(ToolSpec{Name: "ok"}, nil)
	if len(catalog.Names()) != 0 {
		t.Fatalf("invalid registrations should be ignored, got %v", catalog.Names())
	}
}
