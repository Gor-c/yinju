package app

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

type graphNode struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Type   string `json:"type"`
	Weight int    `json:"weight,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type graphEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Type     string `json:"type"`
	Label    string `json:"label,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// unitDocumentStats aggregates document counts by type and the latest index
// time, shared by the profile and the knowledge graph.
func (s *Server) unitDocumentStats(ctx context.Context, unitID string) (map[string]int, int, *time.Time) {
	docTypes := map[string]int{}
	total := 0
	var last *time.Time
	rows, err := s.db.Query(ctx, `SELECT d.doc_type,count(*),max(d.indexed_at)
		FROM documents d JOIN document_units du ON du.doc_id = d.doc_id
		WHERE du.unit_id=$1 GROUP BY d.doc_type`, unitID)
	if err != nil {
		return docTypes, total, last
	}
	defer rows.Close()
	for rows.Next() {
		var docType string
		var count int
		var at *time.Time
		if err = rows.Scan(&docType, &count, &at); err != nil {
			return docTypes, total, last
		}
		if docType == "" {
			docType = "未分类"
		}
		docTypes[docType] += count
		total += count
		if at != nil && (last == nil || at.After(*last)) {
			last = at
		}
	}
	return docTypes, total, last
}

// unitGraph returns a lightweight knowledge graph: project → tags / doc types →
// documents. It is intentionally derived from local metadata only, so the
// feature keeps working without any network access.
func (s *Server) unitGraph(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id := c.Param("id")
	if c.Query("source") == "entity" {
		s.entityGraph(c, id)
		return
	}
	var name, unitType, industry string
	var tagRaw []byte
	err := s.db.QueryRow(c, `SELECT name,unit_type,industry,tags FROM units WHERE unit_id=$1`, id).
		Scan(&name, &unitType, &industry, &tagRaw)
	if err != nil {
		fail(c, err)
		return
	}

	nodes := []graphNode{{ID: "unit:" + id, Label: name, Type: "unit"}}
	edges := []graphEdge{}

	for _, tag := range decodeSkillIDs(tagRaw) {
		nodeID := "tag:" + tag
		nodes = append(nodes, graphNode{ID: nodeID, Label: tag, Type: "tag"})
		edges = append(edges, graphEdge{Source: "unit:" + id, Target: nodeID, Type: "tag"})
	}
	if unitType != "" {
		nodeID := "category:" + unitType
		nodes = append(nodes, graphNode{ID: nodeID, Label: unitType, Type: "category"})
		edges = append(edges, graphEdge{Source: "unit:" + id, Target: nodeID, Type: "category"})
	}
	if industry != "" {
		nodeID := "industry:" + industry
		nodes = append(nodes, graphNode{ID: nodeID, Label: industry, Type: "industry"})
		edges = append(edges, graphEdge{Source: "unit:" + id, Target: nodeID, Type: "industry"})
	}

	// 文档与知识库是多对多（document_units）关系，documents 上没有 unit_id 列。
	rows, err := s.db.Query(c, `SELECT d.filename,d.doc_type,d.chunk_count
		FROM documents d JOIN document_units du ON du.doc_id=d.doc_id
		WHERE du.unit_id=$1 ORDER BY d.filename`, id)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	seenTypes := map[string]bool{}
	for rows.Next() {
		var filename, docType string
		var chunks int
		if err = rows.Scan(&filename, &docType, &chunks); err != nil {
			fail(c, err)
			return
		}
		if docType == "" {
			docType = "未分类"
		}
		typeID := "type:" + docType
		if !seenTypes[docType] {
			seenTypes[docType] = true
			nodes = append(nodes, graphNode{ID: typeID, Label: docType, Type: "doc_type"})
			edges = append(edges, graphEdge{Source: "unit:" + id, Target: typeID, Type: "doc_type"})
		}
		docID := "doc:" + filename
		nodes = append(nodes, graphNode{ID: docID, Label: filename, Type: "document", Weight: chunks})
		edges = append(edges, graphEdge{Source: typeID, Target: docID, Type: "document"})
	}
	if err = rows.Err(); err != nil {
		fail(c, err)
		return
	}

	docTypes, documents, lastIndexed := s.unitDocumentStats(c, id)
	c.JSON(200, gin.H{
		"unit_id": id,
		"nodes":   nodes,
		"edges":   edges,
		"stats": gin.H{
			"documents":       documents,
			"doc_types":       docTypes,
			"last_indexed_at": lastIndexed,
			"nodes":           len(nodes),
			"edges":           len(edges),
			"source":          fmt.Sprintf("local:%s", id),
		},
	})
}
