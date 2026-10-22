package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/vectorstore"
)

// scopedMatch keeps the source project alongside a vector match so multi-project
// chats can attribute citations.
type scopedMatch struct {
	UnitID   string
	UnitName string
	Match    vectorstore.Match
}

// normalizeUnitIDs merges the multi-select field with the legacy single field.
func normalizeUnitIDs(in agentChatRequest) []string {
	values := in.UnitIDs
	if len(values) == 0 && strings.TrimSpace(in.UnitID) != "" {
		values = []string{in.UnitID}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, id := range values {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func containsUnit(ids []string, id string) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

// agentUnitNames loads the display names of every selected project and fails
// when one of them no longer exists.
func (s *Server) agentUnitNames(ctx context.Context, ids []string) (map[string]string, error) {
	names := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	rows, err := s.db.Query(ctx, `SELECT unit_id,name FROM units WHERE unit_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := names[id]; !ok {
			return nil, fmt.Errorf("%w: %s", errUnitNotFound, id)
		}
	}
	return names, nil
}

func (s *Server) unitProfilesScoped(ctx context.Context, ids []string) ([]gin.H, error) {
	out := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		profile, err := s.unitProfile(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, profile)
	}
	return out, nil
}

func (s *Server) hazardsScoped(ctx context.Context, ids []string, status string) ([]gin.H, error) {
	query := `SELECT unit_id,id,hazard,status,severity,source,occurred_at FROM hazards WHERE unit_id = ANY($1)`
	args := []any{ids}
	if status != "" {
		query += ` AND status=$2`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []gin.H{}
	for rows.Next() {
		var unitID string
		var rowID int64
		var hazard, state, severity, source string
		var at any
		if err = rows.Scan(&unitID, &rowID, &hazard, &state, &severity, &source, &at); err != nil {
			return nil, err
		}
		result = append(result, gin.H{
			"unit_id": unitID, "id": rowID, "hazard": hazard, "status": state,
			"severity": severity, "source": source, "occurred_at": at,
		})
	}
	return result, rows.Err()
}

func (s *Server) auditHistoryScoped(ctx context.Context, ids []string) ([]gin.H, error) {
	rows, err := s.db.Query(ctx, `SELECT unit_id,id,title,score,risk_level,payload,created_at
		FROM audit_records WHERE unit_id = ANY($1) ORDER BY created_at DESC`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []gin.H{}
	for rows.Next() {
		var unitID string
		var rowID int64
		var title, risk string
		var score *float64
		var payload []byte
		var created any
		if err = rows.Scan(&unitID, &rowID, &title, &score, &risk, &payload, &created); err != nil {
			return nil, err
		}
		var decoded any
		_ = json.Unmarshal(payload, &decoded)
		result = append(result, gin.H{
			"unit_id": unitID, "id": rowID, "title": title, "score": score,
			"risk_level": risk, "payload": decoded, "created_at": created,
		})
	}
	return result, rows.Err()
}

// searchChunksScoped embeds the query once and merges hits from every selected
// project, keeping the closest k matches overall.
func (s *Server) searchChunksScoped(ctx context.Context, ids []string, names map[string]string, query string, k int) ([]scopedMatch, error) {
	if s.vectors == nil {
		return nil, fmt.Errorf("vector store is not configured")
	}
	if len(ids) == 0 {
		return []scopedMatch{}, nil
	}
	vectors, err := s.embedContext(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("embedding API returned no vectors")
	}
	// 文档是检索单位：把知识库翻译成 doc_id 集合后一次搜索，
	// 再按归属关系给命中结果标注来源知识库。
	docIDs, owner, idErr := s.docIDsForUnitsWithOwner(ctx, ids)
	if idErr != nil {
		return nil, idErr
	}
	if len(docIDs) == 0 {
		return []scopedMatch{}, nil
	}
	matches, searchErr := s.vectors.Search(ctx, docIDs, vectors[0], k)
	if searchErr != nil {
		return nil, searchErr
	}
	all := make([]scopedMatch, 0, len(matches))
	for _, match := range matches {
		unitID := owner[match.DocID]
		all = append(all, scopedMatch{UnitID: unitID, UnitName: names[unitID], Match: match})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Match.Distance < all[j].Match.Distance })
	if len(all) > k {
		all = all[:k]
	}
	return all, nil
}
