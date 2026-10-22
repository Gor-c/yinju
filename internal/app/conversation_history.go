package app

import (
	"context"

	"knowledge-base/internal/logging"
)

// loadConversationHistory 读取某个会话最近的消息，用于在没有客户端 history 时
// 续接上下文。只取 user/assistant 且内容非空的消息，按时间正序返回。
func (s *Server) loadConversationHistory(ctx context.Context, conversationID string, limit int) []agentHistoryMessage {
	if s.db == nil || conversationID == "" || limit <= 0 {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT role, content FROM (
			SELECT id, role, content FROM agent_messages
			WHERE conversation_id = $1 AND role IN ('user','assistant') AND content <> ''
			ORDER BY id DESC
			LIMIT $2
		) recent ORDER BY id`, conversationID, limit)
	if err != nil {
		logging.App().Warn("conversation_history_load_failed", "conversation_id", conversationID, "error", err)
		return nil
	}
	defer rows.Close()

	out := make([]agentHistoryMessage, 0, limit)
	for rows.Next() {
		var item agentHistoryMessage
		if err := rows.Scan(&item.Role, &item.Content); err != nil {
			logging.App().Warn("conversation_history_scan_failed", "conversation_id", conversationID, "error", err)
			return out
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		logging.App().Warn("conversation_history_rows_failed", "conversation_id", conversationID, "error", err)
	}
	return out
}
