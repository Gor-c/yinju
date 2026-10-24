package rag

import "strings"

// Chunker 把提取出的纯文本切成用于嵌入的片段。
//
// 切块是 RAG 质量的第一道闸门：切得碎则语义不完整，切得太长则 embedding
// 语义被稀释、重排也只能看开头。所以策略必须可替换，而不是硬编码一个数字。
type Chunker interface {
	Chunk(text string) []Chunk
	// Name 进入索引元数据，切块策略变更后可以据此判断需不需要重建。
	Name() string
}

// Chunk 是一个待嵌入的片段。
type Chunk struct {
	Index   int
	Content string
}

// FixedChunker 按固定字符数滑动窗口切分，overlap 为相邻片段的重叠字符数。
//
// 它不感知段落与条款边界，因此对条款型文档会切开一条完整规则。
// 这是当前默认策略的已知局限：先保证「不漏内容」，再谈「切得漂亮」。
type FixedChunker struct {
	Size    int
	Overlap int
}

func (c FixedChunker) Name() string {
	return "fixed"
}

func (c FixedChunker) Chunk(text string) []Chunk {
	if c.Size <= 0 {
		return nil
	}
	overlap := c.Overlap
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= c.Size {
		overlap = c.Size / 4
	}
	runes := []rune(strings.TrimSpace(text))
	out := make([]Chunk, 0, len(runes)/c.Size+1)
	for start := 0; start < len(runes); {
		end := start + c.Size
		if end > len(runes) {
			end = len(runes)
		}
		content := strings.TrimSpace(string(runes[start:end]))
		if content != "" {
			out = append(out, Chunk{Index: len(out), Content: content})
		}
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	return out
}
