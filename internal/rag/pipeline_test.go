package rag

import (
	"context"
	"errors"
	"strings"
	"testing"

	"knowledge-base/internal/vectorstore"
)

func testIdentity() Identity { return Identity{Model: "bge-m3", Dim: 1024} }

type stubEmbedder struct {
	vectors [][]float64
	err     error
}

func (s stubEmbedder) Embed(context.Context, []string) ([][]float64, error) {
	return s.vectors, s.err
}

func TestEvidenceOverlap(t *testing.T) {
	related := "光缆敷设时牵引力不得超过光缆允许张力的 80%，牵引速度不宜超过 15m/min"
	if got := evidenceOverlap("光缆敷设时牵引力不得超过多少？", related); got < 0.5 {
		t.Fatalf("related overlap too low: %.3f", got)
	}

	unrelated := "宿） □公共的体育（场）馆、会堂  □建筑面积在200平方米以上的公共娱乐场所"
	if got := evidenceOverlap("该单位的年度营业收入是多少？", unrelated); got > 0.35 {
		t.Fatalf("unrelated overlap too high: %.3f", got)
	}

	// 标点不计入，避免问号、书名号稀释覆盖度。
	withPunct := evidenceOverlap("光缆敷设时，牵引力不得超过多少？", related)
	plain := evidenceOverlap("光缆敷设时牵引力不得超过多少", related)
	if withPunct != plain {
		t.Fatalf("punctuation should not change overlap: %.3f vs %.3f", withPunct, plain)
	}
}

func TestFuseRRFMergesAndDeduplicates(t *testing.T) {
	results := []channelOutcome{
		{matches: []vectorstore.Match{
			{DocID: "doc-a", ChunkIndex: 1, Content: "A1", Distance: 0.2},
			{DocID: "doc-a", ChunkIndex: 2, Content: "A2", Distance: 0.3},
		}},
		{matches: []vectorstore.Match{
			{DocID: "doc-a", ChunkIndex: 2, Content: "A2", Score: 0.5},
			{DocID: "doc-b", ChunkIndex: 0, Content: "B0", Score: 0.4},
		}},
	}

	merged := fuseRRF(results)
	if len(merged) != 3 {
		t.Fatalf("expected 3 fused chunks, got %d", len(merged))
	}
	// 两路都命中的片段排第一。
	if merged[0].DocID != "doc-a" || merged[0].ChunkIndex != 2 {
		t.Fatalf("expected doc-a#2 first, got %s#%d", merged[0].DocID, merged[0].ChunkIndex)
	}
	for i := 1; i < len(merged); i++ {
		if merged[i-1].Score < merged[i].Score {
			t.Fatalf("fused result not sorted by score: %.4f < %.4f", merged[i-1].Score, merged[i].Score)
		}
	}
}

func TestApplyEvidenceGateKeepsConfidentAndRelevant(t *testing.T) {
	pipeline := &Pipeline{Tuning: DefaultTuning()}
	matches := []vectorstore.Match{
		// 重排分很高：即使覆盖率低也保留（同义改写场景）。
		{DocID: "doc-a", ChunkIndex: 0, Content: "完全不同表述的内容", Score: 0.9},
		// 分数低但覆盖率高：保留。
		{DocID: "doc-b", ChunkIndex: 0, Content: "光缆敷设时牵引力不得超过光缆允许张力的 80%", Score: 0.2},
		// 分数低且几乎无覆盖：丢弃。
		{DocID: "doc-c", ChunkIndex: 0, Content: "公司年会定于十二月举行", Score: 0.2},
	}
	kept := pipeline.applyEvidenceGate("光缆敷设时牵引力不得超过多少？", matches, DefaultTuning())
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept chunks, got %d", len(kept))
	}
	for _, m := range kept {
		if m.DocID == "doc-c" {
			t.Fatalf("irrelevant chunk should be filtered")
		}
	}
}

func TestApplyEvidenceGateSkipsShortQueries(t *testing.T) {
	pipeline := &Pipeline{Tuning: DefaultTuning()}
	matches := []vectorstore.Match{{DocID: "doc-a", ChunkIndex: 0, Content: "无关内容", Score: 0.15}}
	kept := pipeline.applyEvidenceGate("负责人是谁", matches, DefaultTuning())
	if len(kept) != 1 {
		t.Fatalf("short query should bypass gate, got %d", len(kept))
	}
}

// 嵌入服务整体不可用时，关键词通道必须独立存活——这是多通道设计的意义。
func TestPipelineSurvivesEmbeddingOutage(t *testing.T) {
	pipeline := &Pipeline{
		Embedder: stubEmbedder{err: errors.New("embedding service down")},
		Identity: testIdentity(),
		Tuning:   DefaultTuning(),
		Channels: []Channel{
			{Name: "vector", Fetch: func(_ context.Context, req Request) ([]Match, error) {
				if len(req.Embedding) == 0 {
					return nil, errors.New("query embedding is unavailable")
				}
				return nil, nil
			}},
			{Name: "keyword", Fetch: func(_ context.Context, _ Request) ([]Match, error) {
				return []Match{{
					DocID: "doc-a", ChunkIndex: 0,
					Content: "光缆敷设时牵引力不得超过光缆允许张力的 80%，牵引速度不宜超过 15m/min",
				}}, nil
			}},
		},
	}
	got, err := pipeline.Search(context.Background(), []string{"doc-a"}, "光缆敷设时牵引力不得超过多少？", 5)
	if err != nil {
		t.Fatalf("keyword-only fallback should not fail: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 keyword hit, got %d", len(got))
	}
}

// 所有通道都挂才判定检索失败，并且错误要能定位根因。
func TestPipelineFailsOnlyWhenEveryChannelFails(t *testing.T) {
	boom := errors.New("pg down")
	pipeline := &Pipeline{
		Identity: testIdentity(),
		Tuning:   DefaultTuning(),
		Channels: []Channel{
			{Name: "vector", Fetch: func(context.Context, Request) ([]Match, error) { return nil, boom }},
			{Name: "keyword", Fetch: func(context.Context, Request) ([]Match, error) { return nil, boom }},
		},
	}
	if _, err := pipeline.Search(context.Background(), []string{"doc-a"}, "任意问题超过八个字", 5); !errors.Is(err, boom) {
		t.Fatalf("expected the underlying error, got %v", err)
	}
}

// 没有达到证据标准时必须返回空，而不是把低分片段硬塞给模型。
func TestPipelineReturnsEmptyWhenEvidenceIsInsufficient(t *testing.T) {
	pipeline := &Pipeline{
		Identity: testIdentity(),
		Tuning:   DefaultTuning(),
		Reranker: fixedReranker{scores: []float64{0.02}},
		Channels: []Channel{
			{Name: "keyword", Fetch: func(context.Context, Request) ([]Match, error) {
				return []Match{{DocID: "doc-a", ChunkIndex: 0, Content: "公司年会定于十二月举行"}}, nil
			}},
		},
	}
	got, err := pipeline.Search(context.Background(), []string{"doc-a"}, "光缆敷设时牵引力不得超过多少？", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no evidence, got %d chunks", len(got))
	}
}

type fixedReranker struct{ scores []float64 }

func (f fixedReranker) Rerank(context.Context, string, []string) ([]float64, error) {
	return f.scores, nil
}

// 重排服务返回的分数条数与候选数不一致时，必须降级而不是错位对齐。
func TestPipelineFallsBackWhenRerankReturnsWrongShape(t *testing.T) {
	pipeline := &Pipeline{
		Identity: testIdentity(),
		Tuning:   DefaultTuning(),
		Reranker: fixedReranker{scores: []float64{0.9, 0.8}},
		Channels: []Channel{
			{Name: "keyword", Fetch: func(context.Context, Request) ([]Match, error) {
				return []Match{{DocID: "doc-a", ChunkIndex: 0, Content: "光缆敷设时牵引力不得超过 80%"}}, nil
			}},
		},
	}
	// 候选只有 1 条，重排却给了 2 个分数。
	got, err := pipeline.Search(context.Background(), []string{"doc-a"}, "光缆敷设时牵引力不得超过多少？", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected fallback to fused order, got %d chunks", len(got))
	}
}

func TestTuningNormalizeKeepsFusionWithinCandidates(t *testing.T) {
	tuning := Tuning{VectorK: 5, KeywordK: 5, FusionK: 100, MaxItems: 200}
	got := tuning.normalize()
	if got.FusionK != 10 {
		t.Fatalf("FusionK should be capped at VectorK+KeywordK, got %d", got.FusionK)
	}
	if got.MaxItems > got.FusionK {
		t.Fatalf("MaxItems must not exceed FusionK, got %d > %d", got.MaxItems, got.FusionK)
	}
}

// 身份不匹配的错误必须能直接告诉运维「跑哪条命令」，否则等于没报。
func TestIdentityMismatchErrorIsActionable(t *testing.T) {
	err := &IdentityMismatch{
		Indexed: Identity{Model: "bge-m3", Dim: 1024},
		Current: Identity{Model: "text-embedding-3-small", Dim: 1536},
		Docs:    12,
	}
	message := err.Error()
	for _, want := range []string{"bge-m3", "text-embedding-3-small", "12", "cmd/reindex"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error message should mention %q, got: %s", want, message)
		}
	}
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("errors.Is should match ErrIdentityMismatch")
	}
}

func TestChunkerProducesOverlappingChunks(t *testing.T) {
	text := strings.Repeat("光缆敷设", 600)
	chunks := FixedChunker{Size: 100, Overlap: 20}.Chunk(text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if chunks[0].Index != 0 || chunks[1].Index != 1 {
		t.Fatalf("chunk indexes must be sequential, got %d and %d", chunks[0].Index, chunks[1].Index)
	}
	for i, chunk := range chunks {
		if runes := []rune(chunk.Content); len(runes) > 100 {
			t.Fatalf("chunk %d exceeds size limit: %d", i, len(runes))
		}
	}
}
