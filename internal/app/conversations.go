package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"knowledge-base/internal/logging"
)

const (
	defaultConversationLimit = 30
	maxConversationLimit     = 100
	conversationTitleRunes   = 40
)

func newConversationID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "conv-" + hex.EncodeToString(buf)
}

// ensureConversation creates the conversation on the first message and bumps
// updated_at on later ones. It returns the id the run should persist under.
func (s *Server) ensureConversation(ctx context.Context, id, unitID string, unitIDs []string, expert, firstMessage string) (string, error) {
	if s.db == nil {
		return "", errDatabaseNotConfigured
	}
	if len(unitIDs) > 0 {
		unitID = unitIDs[0]
	}
	encodedUnits := encodeSkillIDs(unitIDs)
	id = strings.TrimSpace(id)
	if id != "" {
		tag, err := s.db.Exec(ctx, `UPDATE agent_conversations SET updated_at=now(),unit_id=$2,unit_ids=$3 WHERE conversation_id=$1`,
			id, unitID, encodedUnits)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() > 0 {
			return id, nil
		}
	} else {
		id = newConversationID()
	}
	title := strings.TrimSpace(firstMessage)
	if title == "" {
		title = "新对话"
	}
	_, err := s.db.Exec(ctx, `INSERT INTO agent_conversations(conversation_id,unit_id,unit_ids,expert,title)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (conversation_id) DO UPDATE SET updated_at=now(),unit_id=excluded.unit_id,unit_ids=excluded.unit_ids`,
		id, unitID, encodedUnits, expert, truncateRunes(title, conversationTitleRunes))
	return id, err
}

// appendConversationMessage is best-effort: history persistence must never
// break a chat response.
func (s *Server) appendConversationMessage(ctx context.Context, conversationID, role, content string, citations []citation, runID string) {
	if s.db == nil || conversationID == "" {
		return
	}
	if citations == nil {
		citations = []citation{}
	}
	raw, _ := json.Marshal(citations)
	// 客户端中途断开时请求上下文会被取消，历史仍应落库。
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.db.Exec(saveCtx, `INSERT INTO agent_messages(conversation_id,role,content,citations,run_id)
		VALUES($1,$2,$3,$4,$5)`, conversationID, role, content, json.RawMessage(raw), runID); err != nil {
		logging.App().Warn("conversation_message_save_failed",
			"conversation_id", conversationID, "role", role, "error", err)
	}
}

func (s *Server) conversationList(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	limit := defaultConversationLimit
	if value, err := strconv.Atoi(c.Query("limit")); err == nil && value > 0 && value <= maxConversationLimit {
		limit = value
	}
	unitID := strings.TrimSpace(c.Query("unit_id"))
	rows, err := s.db.Query(c, `SELECT c.conversation_id,c.unit_id,c.unit_ids,c.expert,c.title,c.created_at,c.updated_at,
		(SELECT count(*) FROM agent_messages m WHERE m.conversation_id=c.conversation_id),
		COALESCE((SELECT m.content FROM agent_messages m WHERE m.conversation_id=c.conversation_id ORDER BY m.id DESC LIMIT 1),'')
		FROM agent_conversations c
		WHERE ($1='' OR c.unit_id=$1 OR c.unit_ids ? $1)
		ORDER BY c.updated_at DESC
		LIMIT $2`, unitID, limit)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, unit, expert, title, preview string
		var unitRaw []byte
		var createdAt, updatedAt time.Time
		var count int
		if err = rows.Scan(&id, &unit, &unitRaw, &expert, &title, &createdAt, &updatedAt, &count, &preview); err != nil {
			fail(c, err)
			return
		}
		out = append(out, gin.H{
			"conversation_id": id,
			"unit_id":         unit,
			"unit_ids":        decodeSkillIDs(unitRaw),
			"expert":          expert,
			"title":           title,
			"message_count":   count,
			"preview":         truncateRunes(preview, 80),
			"created_at":      createdAt,
			"updated_at":      updatedAt,
		})
	}
	if err = rows.Err(); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, out)
}

func (s *Server) conversationDetail(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	id := c.Param("id")
	var unitID, expert, title string
	var unitRaw []byte
	var createdAt, updatedAt time.Time
	err := s.db.QueryRow(c, `SELECT unit_id,unit_ids,expert,title,created_at,updated_at
		FROM agent_conversations WHERE conversation_id=$1`, id).
		Scan(&unitID, &unitRaw, &expert, &title, &createdAt, &updatedAt)
	if err != nil {
		fail(c, err)
		return
	}
	rows, err := s.db.Query(c, `SELECT id,role,content,citations,run_id,created_at
		FROM agent_messages WHERE conversation_id=$1 ORDER BY id`, id)
	if err != nil {
		fail(c, err)
		return
	}
	defer rows.Close()
	messages := []gin.H{}
	for rows.Next() {
		var messageID int64
		var role, content, runID string
		var citations []byte
		var at any
		if err = rows.Scan(&messageID, &role, &content, &citations, &runID, &at); err != nil {
			fail(c, err)
			return
		}
		items := []citation{}
		_ = json.Unmarshal(citations, &items)
		messages = append(messages, gin.H{
			"id":         messageID,
			"role":       role,
			"content":    content,
			"citations":  items,
			"run_id":     runID,
			"created_at": at,
		})
	}
	if err = rows.Err(); err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{
		"conversation_id": id,
		"unit_id":         unitID,
		"unit_ids":        decodeSkillIDs(unitRaw),
		"expert":          expert,
		"title":           title,
		"created_at":      createdAt,
		"updated_at":      updatedAt,
		"messages":        messages,
	})
}

func (s *Server) deleteConversation(c *gin.Context) {
	if !s.ready(c) {
		return
	}
	tag, err := s.db.Exec(c, `DELETE FROM agent_conversations WHERE conversation_id=$1`, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	c.JSON(200, gin.H{"deleted": c.Param("id")})
}
