package app

import "testing"

func TestUnverifiedFactsFlagsMissingNumbers(t *testing.T) {
	citations := []citation{
		{Content: "第二十七条 单个接头损耗不大于 0.08dB，中继段内接头平均损耗不大于 0.05dB。"},
	}
	answer := "单个接头损耗不大于 0.08dB，平均 0.05dB；另有 70% 的设备需要更换。"

	got := unverifiedFacts(answer, citations)
	if len(got) != 1 {
		t.Fatalf("expected exactly one unverified fact, got %v", got)
	}
	if got[0] != "70%" {
		t.Fatalf("expected 70%%, got %q", got[0])
	}
}

func TestUnverifiedFactsIgnoresShortNumbers(t *testing.T) {
	citations := []citation{{Content: "第一条 归档要求。"}}
	// 单字符数字（序号）不参与校验，避免噪声。
	if got := unverifiedFacts("第 2 条要求，共 3 类", citations); len(got) != 0 {
		t.Fatalf("short numbers should be ignored, got %v", got)
	}
}

func TestUnverifiedFactsWithoutCitations(t *testing.T) {
	if got := unverifiedFacts("设备 70% 需要更换", nil); got != nil {
		t.Fatalf("no citations means no verification, got %v", got)
	}
	if got := unverifiedFacts("", []citation{{Content: "x"}}); got != nil {
		t.Fatalf("empty answer should return nil, got %v", got)
	}
}

func TestUnverifiedFactsMatchesPercentVariant(t *testing.T) {
	citations := []citation{{Content: "牵引力不得超过光缆允许张力的 80"}}
	// 片段里没有百分号但数值存在，视为已核实。
	if got := unverifiedFacts("不得超过 80%", citations); len(got) != 0 {
		t.Fatalf("percent variant should match, got %v", got)
	}
}
