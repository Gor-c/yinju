package app

import (
	"regexp"
	"strings"
)

// factPattern 抓取答案中的数值事实：整数、小数、百分比。
// 中文数字（「三月底」）不做处理，量词与序号靠长度过滤。
var factPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?%?`)

// unverifiedFacts 检查答案里的数值是否出现在引用片段中。
// 模型可能跨片段推算，因此只作为提示，不阻断回答。
// 返回最多 8 个未命中的数值。
func unverifiedFacts(answer string, citations []citation) []string {
	if strings.TrimSpace(answer) == "" || len(citations) == 0 {
		return nil
	}
	var corpus strings.Builder
	for _, c := range citations {
		corpus.WriteString(c.Content)
		corpus.WriteString("\n")
	}
	text := corpus.String()

	seen := map[string]struct{}{}
	out := []string{}
	for _, fact := range factPattern.FindAllString(answer, -1) {
		// 单字符数字噪声太大（第 2 条、3 个），只校验两位数以上。
		if len(strings.TrimSuffix(fact, "%")) < 2 {
			continue
		}
		if _, dup := seen[fact]; dup {
			continue
		}
		seen[fact] = struct{}{}
		if strings.Contains(text, fact) {
			continue
		}
		// 去掉百分号再试一次：片段里可能写成「80 %」或纯数字。
		if strings.Contains(text, strings.TrimSuffix(fact, "%")) {
			continue
		}
		out = append(out, fact)
		if len(out) >= 8 {
			break
		}
	}
	return out
}
