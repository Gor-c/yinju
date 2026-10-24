// Command eval 跑检索评测：调用 POST /api/v1/retrieve（只检索、不生成），
// 输出命中率与拒答准确率。调检索参数前后各跑一次即可对比效果。
//
//	go run ./cmd/eval
//	go run ./cmd/eval -base http://127.0.0.1:8081 -file eval/questions.json -topk 5
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type testCase struct {
	ID               string   `json:"id"`
	UnitID           string   `json:"unit_id"`
	Question         string   `json:"question"`
	ExpectFiles      []string `json:"expect_files"`
	ExpectContent    []string `json:"expect_content"`
	ExpectNoEvidence bool     `json:"expect_no_evidence"`
	// ExpectAnswerRefusal 表示检索到相关片段但资料不足，需要生成层明确说「没有提到」。
	ExpectAnswerRefusal bool `json:"expect_answer_refusal"`
}

type suite struct {
	Description string     `json:"description"`
	Cases       []testCase `json:"cases"`
}

type match struct {
	DocID      string  `json:"doc_id"`
	Filename   string  `json:"filename"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Distance   float64 `json:"distance"`
	Score      float64 `json:"score"`
}

type retrieveResponse struct {
	Count      int     `json:"count"`
	Matches    []match `json:"matches"`
	NoEvidence bool    `json:"no_evidence"`
}

func main() {
	base := flag.String("base", "http://127.0.0.1:8081", "服务地址")
	file := flag.String("file", "eval/questions.json", "评测集路径")
	topK := flag.Int("topk", 5, "每条用例取回多少片段")
	withAnswer := flag.Bool("answer", false, "对 expect_answer_refusal 用例调用 /ask 验证生成层拒答（较慢）")
	flag.Parse()

	raw, err := os.ReadFile(*file)
	if err != nil {
		fmt.Println("读取评测集失败:", err)
		os.Exit(2)
	}
	var s suite
	if err = json.Unmarshal(raw, &s); err != nil {
		fmt.Println("解析评测集失败:", err)
		os.Exit(2)
	}
	if len(s.Cases) == 0 {
		fmt.Println("评测集为空")
		os.Exit(2)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	passed, retrievalPassed, refusalPassed, answerPassed, skipped := 0, 0, 0, 0, 0
	retrievalTotal, refusalTotal, answerTotal := 0, 0, 0
	var totalLatency time.Duration

	fmt.Printf("%-8s %-6s %-8s %s\n", "用例", "结果", "耗时", "说明")
	for _, c := range s.Cases {
		if c.ExpectAnswerRefusal && !*withAnswer {
			skipped++
			fmt.Printf("%-8s %-6s %-8s %s\n", c.ID, "SKIP", "—", "生成层用例，加 -answer 运行")
			continue
		}
		started := time.Now()
		resp, err := retrieve(client, *base, c, *topK)
		cost := time.Since(started)
		totalLatency += cost

		if err != nil {
			fmt.Printf("%-8s %-6s %-8s %s\n", c.ID, "ERROR", "—", err)
			continue
		}
		ok, detail := judge(c, resp)
		if c.ExpectAnswerRefusal {
			ok, detail, err = judgeAnswer(client, *base, c, *topK)
			if err != nil {
				fmt.Printf("%-8s %-6s %-8s %s\n", c.ID, "ERROR", "—", err)
				continue
			}
		}
		if ok {
			passed++
			switch {
			case c.ExpectNoEvidence:
				refusalPassed++
			case c.ExpectAnswerRefusal:
				answerPassed++
			default:
				retrievalPassed++
			}
		}
		switch {
		case c.ExpectNoEvidence:
			refusalTotal++
		case c.ExpectAnswerRefusal:
			answerTotal++
		default:
			retrievalTotal++
		}
		status := "PASS"
		if !ok {
			status = "FAIL"
		}
		fmt.Printf("%-8s %-6s %-8s %s\n", c.ID, status, cost.Round(time.Millisecond), detail)
	}

	fmt.Println()
	fmt.Printf("检索用例：%d/%d 通过\n", retrievalPassed, retrievalTotal)
	fmt.Printf("检索层拒答：%d/%d 通过\n", refusalPassed, refusalTotal)
	if answerTotal > 0 {
		fmt.Printf("生成层拒答：%d/%d 通过\n", answerPassed, answerTotal)
	}
	fmt.Printf("总计：%d/%d 通过，平均耗时 %s\n",
		passed, len(s.Cases), (totalLatency / time.Duration(len(s.Cases))).Round(time.Millisecond))
	if skipped > 0 {
		fmt.Printf("跳过：%d 条（需 -answer）\n", skipped)
	}
	if passed+skipped < len(s.Cases) {
		os.Exit(1)
	}
}

func retrieve(client *http.Client, base string, c testCase, topK int) (retrieveResponse, error) {
	body, _ := json.Marshal(map[string]any{"unit_id": c.UnitID, "query": c.Question, "top_k": topK})
	res, err := client.Post(strings.TrimRight(base, "/")+"/api/v1/retrieve", "application/json", bytes.NewReader(body))
	if err != nil {
		return retrieveResponse{}, err
	}
	defer res.Body.Close()
	var out retrieveResponse
	if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
		return retrieveResponse{}, fmt.Errorf("解析响应失败（HTTP %d）", res.StatusCode)
	}
	return out, nil
}

// judgeAnswer 调用 /ask 验证生成层：资料不足时必须明确说明，而不是编造。
func judgeAnswer(client *http.Client, base string, c testCase, topK int) (bool, string, error) {
	body, _ := json.Marshal(map[string]any{"unit_id": c.UnitID, "question": c.Question, "top_k": topK})
	res, err := client.Post(strings.TrimRight(base, "/")+"/api/v1/ask", "application/json", bytes.NewReader(body))
	if err != nil {
		return false, "", err
	}
	defer res.Body.Close()
	var out struct {
		Answer     string `json:"answer"`
		NoEvidence bool   `json:"no_evidence"`
	}
	if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
		return false, "", fmt.Errorf("解析响应失败（HTTP %d）", res.StatusCode)
	}
	if out.NoEvidence {
		return true, "检索层已拒答", nil
	}
	for _, phrase := range []string{"没有找到", "未提及", "没有提到", "无法回答", "没有相关", "未找到", "资料中没有", "没有涉及"} {
		if strings.Contains(out.Answer, phrase) {
			return true, "生成层明确说明资料不足", nil
		}
	}
	return false, "回答未明确说明资料不足: " + truncate(out.Answer, 60), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// judge 判定单条用例：拒答类要求零命中；检索类要求文件名命中且片段包含全部关键词。
func judge(c testCase, resp retrieveResponse) (bool, string) {
	if c.ExpectNoEvidence {
		if len(resp.Matches) == 0 {
			return true, "正确拒答"
		}
		return false, fmt.Sprintf("应拒答，却命中 %d 条（%s 第 %d 块）",
			len(resp.Matches), resp.Matches[0].Filename, resp.Matches[0].ChunkIndex)
	}
	if len(resp.Matches) == 0 {
		return false, "没有召回到任何片段"
	}

	fileHit := len(c.ExpectFiles) == 0
	for _, m := range resp.Matches {
		for _, want := range c.ExpectFiles {
			if m.Filename == want {
				fileHit = true
			}
		}
	}

	var joined strings.Builder
	for _, m := range resp.Matches {
		joined.WriteString(m.Content)
		joined.WriteString("\n")
	}
	text := joined.String()
	missing := []string{}
	for _, kw := range c.ExpectContent {
		if !strings.Contains(text, kw) {
			missing = append(missing, kw)
		}
	}
	if !fileHit {
		return false, fmt.Sprintf("文件未命中（首位 %s 第 %d 块）", resp.Matches[0].Filename, resp.Matches[0].ChunkIndex)
	}
	if len(missing) > 0 {
		return false, "片段缺少关键词: " + strings.Join(missing, "、")
	}
	return true, fmt.Sprintf("命中 %s 第 %d 块（score %.3f）",
		resp.Matches[0].Filename, resp.Matches[0].ChunkIndex, resp.Matches[0].Score)
}
