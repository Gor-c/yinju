// Package rag 是检索链路的唯一边界，按层切分为四个可替换组件：
//
//	Chunker   文本 → 片段        （切块策略）
//	Embedder  文本 → 向量        （嵌入服务）
//	Searcher  向量/关键词 → 候选  （召回来源，可并行多路）
//	Reranker  查询+片段 → 分数    （精排）
//
// Pipeline 负责编排，并持有三件不属于任何单一组件的东西：
// 融合（RRF）、证据判定（阈值 + 覆盖率闸门）、失败降级路径。
//
// 这个包不读环境变量、不碰数据库、不感知 HTTP：所有外部依赖以接口注入，
// 因此每条链路都可以在单元测试里用假实现跑通。
package rag

import (
	"context"
	"errors"
	"fmt"

	"knowledge-base/internal/vectorstore"
)

// Match 是检索命中的片段。直接复用 vectorstore.Match，避免在两个包之间
// 定义同构结构再转换一次——方向是 rag → vectorstore，不反向依赖。
type Match = vectorstore.Match

// Identity 是向量空间的身份。两个 Identity 不同的向量**不可比较**：
// 用 A 模型写入的向量拿 B 模型的查询向量去算余弦，得到的是噪声而不是相似度。
//
// 这不是理论风险：换模型时没有任何报错，只表现为检索质量整体下滑，
// 因此每次检索前都必须显式比对，不匹配就拒绝服务而不是降级。
type Identity struct {
	Model string
	Dim   int
}

func (i Identity) Validate() error {
	if i.Model == "" {
		return errors.New("嵌入模型未配置：请在「设置 > 模型服务」选择来源，或设置 EMBED_MODEL 环境变量")
	}
	if i.Dim <= 0 {
		return fmt.Errorf("嵌入维度无效（%d）：请检查 EMBED_DIMENSIONS", i.Dim)
	}
	return nil
}

func (i Identity) String() string {
	return fmt.Sprintf("%s@%d", i.Model, i.Dim)
}

func (i Identity) Equal(other Identity) bool {
	return i.Model == other.Model && i.Dim == other.Dim
}

// ErrIdentityMismatch 供上层用 errors.Is 判断，决定返回 503 而不是 500。
var ErrIdentityMismatch = errors.New("embedding identity mismatch")

// IdentityMismatch 描述一次不可比较的检索，并给出可操作的修复动作。
// 错误信息会直接回给调用方，因此必须说清「用什么建的索引、现在是什么、
// 该跑哪条命令」，而不是只说 mismatch。
type IdentityMismatch struct {
	Indexed Identity
	Current Identity
	Docs    int
}

func (e *IdentityMismatch) Error() string {
	return fmt.Sprintf(
		"索引由嵌入模型 %s 生成（%d 个文档），当前配置为 %s，两套向量不可比较。"+
			"请运行 go run ./cmd/reindex 重建索引，或把嵌入模型改回 %s",
		e.Indexed, e.Docs, e.Current, e.Indexed.Model)
}

func (e *IdentityMismatch) Unwrap() error { return ErrIdentityMismatch }

// Embedder 把文本转成向量。实现方负责超时、重试与错误分类。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// Searcher 是向量召回通道。docIDs 为空表示没有可检索范围，实现方应返回空。
type Searcher interface {
	Search(ctx context.Context, docIDs []string, embedding []float64, limit int) ([]Match, error)
}

// KeywordSearcher 是关键词召回通道。它与向量通道并行执行、互为降级。
type KeywordSearcher interface {
	Search(ctx context.Context, docIDs []string, query string, limit int) ([]Match, error)
}

// Reranker 只负责打分，不负责阈值：阈值属于策略，留在 Pipeline 里，
// 这样换一个重排服务不需要重新约定「低分多少算没依据」。
//
// 返回的分数切片必须与 docs 等长。实现不可用时返回错误，Pipeline 会
// 自动降级到融合顺序 + 距离兜底，而不是让整次检索失败。
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// Tuning 是一次检索的全部可调参数。集中放在这里的原因：
// 这些值互相约束（fusionK 必须 ≤ vectorK+keywordK，minOverlap 与
// rerank 阈值共同决定拒答边界），散落在各处最容易调崩。
type Tuning struct {
	VectorK    int
	KeywordK   int
	FusionK    int
	MaxItems   int     // 送重排的候选上限
	MinScore   float64 // 重排分数低于此值即丢弃
	MaxDist    float64 // 重排不可用时的向量距离兜底
	MinOverlap float64 // 二元组覆盖率闸门，0 表示关闭
}

// DefaultTuning 是保守默认值，与调优前的线上行为一致。
func DefaultTuning() Tuning {
	return Tuning{
		VectorK:    30,
		KeywordK:   30,
		FusionK:    20,
		MaxItems:   24,
		MinScore:   0.1,
		MaxDist:    0.55,
		MinOverlap: 0.28,
	}
}

// normalize 兜住互相矛盾的配置，避免出现「融合后 5 条但要重排 24 条」这类
// 静默截断。
func (t Tuning) normalize() Tuning {
	if t.VectorK < 5 {
		t.VectorK = 5
	}
	if t.KeywordK < 5 {
		t.KeywordK = 5
	}
	if t.FusionK < 5 {
		t.FusionK = 5
	}
	ceiling := t.VectorK + t.KeywordK
	if t.FusionK > ceiling {
		t.FusionK = ceiling
	}
	if t.MaxItems <= 0 || t.MaxItems > t.FusionK {
		t.MaxItems = t.FusionK
	}
	return t
}
