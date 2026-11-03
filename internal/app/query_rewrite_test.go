package app

import (
	"reflect"
	"testing"
)

func TestParseRewriteOutputJSON(t *testing.T) {
	got := parseRewriteOutput(`{"queries":["国产化 信创 安全可靠改造","信创 自主可控 重点任务"]}`, "国产化重点内容是哪些")
	want := []string{"国产化 信创 安全可靠改造", "信创 自主可控 重点任务"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected: %#v", got)
	}
}

func TestParseRewriteOutputFenced(t *testing.T) {
	raw := "```json\n{\"queries\":[\"适配要求 达梦数据库\"]}\n```"
	got := parseRewriteOutput(raw, "适配要求有哪些")
	if len(got) != 1 || got[0] != "适配要求 达梦数据库" {
		t.Fatalf("fenced json not parsed: %#v", got)
	}
}

func TestParseRewriteOutputLines(t *testing.T) {
	raw := "1. 光缆敷设 牵引力\n2. 弯曲半径 光缆外径\n"
	got := parseRewriteOutput(raw, "光缆敷设要求有哪些")
	if len(got) != 2 {
		t.Fatalf("expected 2 queries, got %#v", got)
	}
}

func TestParseRewriteOutputFiltersNoise(t *testing.T) {
	// 原问题本身、空串、超长串都不应成为检索式。
	long := ""
	for i := 0; i < 80; i++ {
		long += "长"
	}
	raw := `{"queries":["国产化重点内容是哪些","","a"]}` + "\n" + long
	got := parseRewriteOutput(raw, "国产化重点内容是哪些")
	for _, q := range got {
		if q == "国产化重点内容是哪些" {
			t.Fatal("original question should be filtered out")
		}
		if len([]rune(q)) < 2 || len([]rune(q)) > 60 {
			t.Fatalf("query length out of range: %q", q)
		}
	}
}
