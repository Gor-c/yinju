package rag

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Request 是一次召回请求。用结构体而不是位置参数，是为了以后加过滤条件
// （时间范围、文档类型、标签）时不必改动每个通道的实现。
type Request struct {
	Query  string
	DocIDs []string
	// Embedding 可能为 nil：嵌入层失败时，向量通道自行返回错误，
	// 关键词通道不受影响——这正是降级路径的落点。
	Embedding []float64
}

// Channel 是一路召回。把通道抽象成数据而不是写死的两段代码，
// 是为了让「再加一路召回」变成 append 一行，而不是改动编排逻辑。
type Channel struct {
	Name  string
	Fetch func(ctx context.Context, req Request) ([]Match, error)
}

// Stats 是一次检索的分层计数，用于留痕与调参。
//
// 只看「答了/没答」无法定位问题：要能看出是召回没捞到、融合排错、还是
// 阈值把好片段杀掉了——三层各自的数字才有诊断价值。
type Stats struct {
	VectorHits  int
	KeywordHits int
	Fused       int
	Reranked    int
	Kept        int
	Degraded    []string
}

// Pipeline 编排一次完整检索。它不持有任何外部连接，只持有接口。
type Pipeline struct {
	Embedder Embedder
	Channels []Channel
	Reranker Reranker
	Tuning   Tuning
	Identity Identity

	// OnDegrade 在某个通道或重排失败降级时回调，供上层打点。
	// 用回调而不是直接依赖日志包，是为了这个包能在测试里零副作用运行。
	OnDegrade func(channel string, err error)

	// OnTrace 在一次检索结束时回调分层计数。用于把「为什么拒答」写进留痕，
	// 而不是让用户对着一个空结果猜。
	OnTrace func(stats Stats)
}

func (p *Pipeline) degrade(channel string, err error) {
	if p.OnDegrade != nil && err != nil {
		p.OnDegrade(channel, err)
	}
}

// Search 执行一次检索。返回空切片表示「没有达到证据标准」，
// 这是正常结果而非错误——调用方必须据此拒答，不能拿空结果去调模型。
func (p *Pipeline) Search(ctx context.Context, docIDs []string, query string, k int) ([]Match, error) {
	if len(docIDs) == 0 || k < 1 || strings.TrimSpace(query) == "" {
		return []Match{}, nil
	}
	if len(p.Channels) == 0 {
		return nil, errors.New("rag: no retrieval channel configured")
	}
	if err := p.Identity.Validate(); err != nil {
		return nil, err
	}
	tuning := p.Tuning.normalize()

	// 关键词召回与证据闸门使用去掉疑问词的核心检索式：
	// 「国产化重点内容是哪些」和「国产化重点内容」应当得到同样结果，
	// 否则同一意图会因为有疑问尾巴而被拒答。向量召回仍用原句。
	searchQuery := normalizeQuery(query)

	// 嵌入只做一次，所有通道共用，避免多路召回各自重复调用嵌入服务。
	req := Request{Query: searchQuery, DocIDs: docIDs}
	if p.Embedder != nil {
		vectors, err := p.Embedder.Embed(ctx, []string{query})
		if err != nil {
			p.degrade("embed", err)
		} else if len(vectors) == 0 {
			p.degrade("embed", errors.New("embedding API returned no vectors"))
		} else {
			req.Embedding = vectors[0]
		}
	}

	results := make([]channelOutcome, len(p.Channels))
	var wg sync.WaitGroup
	for i := range p.Channels {
		channel := p.Channels[i]
		wg.Add(1)
		go func(index int, ch Channel) {
			defer wg.Done()
			defer func() {
				// 一路通道 panic 不能带走整次检索。
				if recovered := recover(); recovered != nil {
					results[index] = channelOutcome{err: fmt.Errorf("channel %s panicked: %v", ch.Name, recovered)}
				}
			}()
			hits, err := ch.Fetch(ctx, req)
			results[index] = channelOutcome{matches: hits, err: err}
		}(i, channel)
	}
	wg.Wait()

	// 只要还有通道可用就继续：单路故障是多通道设计的意义所在。
	if everyFailed(results) {
		if p.OnTrace != nil {
			stats := Stats{}
			for _, ch := range p.Channels {
				stats.Degraded = append(stats.Degraded, ch.Name)
			}
			p.OnTrace(stats)
		}
		return nil, firstError(results)
	}
	stats := Stats{}
	for i, item := range results {
		if item.err != nil {
			// 失败的那一路也要让上层看见，否则「为什么只剩关键词结果」无从排查。
			p.degrade(p.Channels[i].Name, item.err)
			stats.Degraded = append(stats.Degraded, p.Channels[i].Name)
			continue
		}
		switch p.Channels[i].Name {
		case "vector":
			stats.VectorHits = len(item.matches)
		case "keyword":
			stats.KeywordHits = len(item.matches)
		}
	}

	fused := fuseRRF(results)
	if len(fused) > tuning.FusionK {
		fused = fused[:tuning.FusionK]
	}
	stats.Fused = len(fused)

	ranked := p.rerank(ctx, query, fused, tuning)
	stats.Reranked = len(ranked)
	ranked = p.applyEvidenceGate(searchQuery, ranked, tuning)
	if len(ranked) > k {
		ranked = ranked[:k]
	}
	stats.Kept = len(ranked)

	if p.OnTrace != nil {
		p.OnTrace(stats)
	}
	return ranked, nil
}

// channelOutcome 是一路召回的运行结果。
type channelOutcome struct {
	matches []Match
	err     error
}

func everyFailed(results []channelOutcome) bool {
	for _, item := range results {
		if item.err == nil {
			return false
		}
	}
	return true
}

// firstError 取第一个真实错误用于定位根因，而不是笼统报「检索失败」。
func firstError(results []channelOutcome) error {
	for _, item := range results {
		if item.err != nil {
			return item.err
		}
	}
	return errors.New("rag: all retrieval channels failed without an error")
}

// rerank 精排并按阈值过滤。任何失败都回退到融合顺序 + 距离兜底：
// 让一次检索降级为「精度略低但仍然可用」，而不是整次问答失败。
func (p *Pipeline) rerank(ctx context.Context, query string, candidates []Match, tuning Tuning) []Match {
	if p.Reranker == nil || len(candidates) == 0 {
		return filterByDistance(candidates, tuning.MaxDist)
	}
	limit := len(candidates)
	if limit > tuning.MaxItems {
		limit = tuning.MaxItems
	}
	docs := make([]string, 0, limit)
	for _, c := range candidates[:limit] {
		docs = append(docs, c.Content)
	}
	scores, err := p.Reranker.Rerank(ctx, query, docs)
	if err != nil {
		p.degrade("rerank", err)
		return filterByDistance(candidates, tuning.MaxDist)
	}
	if len(scores) != len(docs) {
		p.degrade("rerank", fmt.Errorf("rerank returned %d scores for %d documents", len(scores), len(docs)))
		return filterByDistance(candidates, tuning.MaxDist)
	}

	scored := make([]Match, 0, limit)
	for i, score := range scores {
		match := candidates[i]
		match.Score = score
		scored = append(scored, match)
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	kept := scored[:0]
	for _, match := range scored {
		if match.Score < tuning.MinScore {
			continue
		}
		kept = append(kept, match)
	}
	return kept
}

// GateByQuery 用指定问题再过滤一次结果。
// 多检索式合并后使用：召回可以靠改写放宽，但「片段到底支不支持这个提问」
// 必须以用户原问题为准，否则改写出来的同义词会把弱相关片段带进上下文。
func (p *Pipeline) GateByQuery(query string, matches []Match) []Match {
	return p.applyEvidenceGate(query, matches, p.Tuning.normalize())
}

// applyEvidenceGate 是重排之后的第二道闸门。
//
// 重排分数是模型给出的连续值，阈值附近的行为不稳定；而「问题里的字在片段里
// 到底出现过没有」是确定性信号。用二元组覆盖率兜住阈值附近的边界，
// 比单纯调阈值更抗分布漂移。
func (p *Pipeline) applyEvidenceGate(query string, matches []Match, tuning Tuning) []Match {
	if tuning.MinOverlap <= 0 || len([]rune(foldText(query))) < minQueryRunesForGate {
		return matches
	}
	out := matches[:0]
	for _, match := range matches {
		// 重排很确信时不再用覆盖率否掉它，避免误杀同义改写。
		if match.Score >= confidentRerankScore {
			out = append(out, match)
			continue
		}
		if evidenceOverlap(query, match.Content) < tuning.MinOverlap {
			continue
		}
		out = append(out, match)
	}
	return out
}

const (
	rrfSmoothing = 60.0
	// minQueryRunesForGate：查询太短时二元组覆盖率不稳定，不参与判定。
	minQueryRunesForGate = 8
	// confidentRerankScore：重排分数足够高时不再做覆盖度校验。
	confidentRerankScore = 0.5
)

// fuseRRF 用 Reciprocal Rank Fusion 合并多路结果：不校准分数量纲，
// 只依赖各自排名，因此新增一路召回不需要重新调权重。
func fuseRRF(results []channelOutcome) []Match {
	type entry struct {
		match Match
		score float64
	}
	merged := map[string]*entry{}
	order := []string{}
	for _, item := range results {
		if item.err != nil {
			continue
		}
		for i, hit := range item.matches {
			key := matchKey(hit)
			existing, ok := merged[key]
			if !ok {
				existing = &entry{match: hit}
				merged[key] = existing
				order = append(order, key)
			} else if existing.match.Filename == "" {
				// 关键词命中可能缺少文件名（文档已删），保留信息更全的一条。
				existing.match.Filename = hit.Filename
				existing.match.DocType = hit.DocType
				existing.match.ChunkIndex = hit.ChunkIndex
			}
			existing.score += 1 / (rrfSmoothing + float64(i+1))
		}
	}

	out := make([]Match, 0, len(order))
	for _, key := range order {
		item := merged[key]
		item.match.Score = item.score
		out = append(out, item.match)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func matchKey(match Match) string {
	if match.ChunkIndex >= 0 && match.DocID != "" {
		return match.DocID + "#" + strconv.Itoa(match.ChunkIndex)
	}
	sum := sha1.Sum([]byte(match.Content))
	return "text:" + hex.EncodeToString(sum[:8])
}

func filterByDistance(matches []Match, max float64) []Match {
	if max <= 0 {
		return matches
	}
	out := matches[:0]
	for _, match := range matches {
		// Distance 为 0 表示该命中不来自向量通道（如纯关键词），不做距离判定。
		if match.Distance > 0 && match.Distance > max {
			continue
		}
		out = append(out, match)
	}
	return out
}

// queryStopPhrases 是问句里不携带检索信息的成分。它们会稀释二元组覆盖率，
// 也会拉低关键词相似度，导致「国产化重点内容是哪些」被拒答而
// 「国产化重点内容」能命中——同一意图因多了一个疑问尾巴得到相反结果。
var queryStopPhrases = []string{
	"是什么意思", "有哪些", "是哪些", "是什么", "怎么样", "为什么", "怎么",
	"哪些", "哪个", "什么", "如何", "请问", "请帮我", "帮我", "告诉我",
	"介绍一下", "说明一下", "一下", "麻烦", "谢谢",
}

// normalizeQuery 去掉疑问与客套成分，得到用于关键词召回与证据校验的核心检索式。
func normalizeQuery(query string) string {
	out := strings.TrimSpace(query)
	for _, phrase := range queryStopPhrases {
		out = strings.ReplaceAll(out, phrase, "")
	}
	if len([]rune(strings.TrimSpace(foldText(out)))) < 2 {
		return query
	}
	return out
}

// evidenceOverlap 计算问题二元组在片段中的覆盖比例（忽略标点与大小写）。
func evidenceOverlap(query, content string) float64 {
	grams := bigrams(query)
	if len(grams) == 0 {
		return 1
	}
	text := foldText(content)
	hit := 0
	for gram := range grams {
		if strings.Contains(text, gram) {
			hit++
		}
	}
	return float64(hit) / float64(len(grams))
}

func bigrams(s string) map[string]struct{} {
	runes := []rune(foldText(s))
	if len(runes) < 2 {
		return nil
	}
	out := make(map[string]struct{}, len(runes)-1)
	for i := 0; i+1 < len(runes); i++ {
		out[string(runes[i:i+2])] = struct{}{}
	}
	return out
}

// foldText 去掉标点与空白：中文按字符匹配，标点会稀释覆盖度。
func foldText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
