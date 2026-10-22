package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"knowledge-base/internal/logging"
	"knowledge-base/internal/vectorstore"
)

const (
	modernMCPVersion = "2026-07-28"
	legacyMCPVersion = "2025-11-25"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) mcp(c *gin.Context) {
	if !s.authorizeMCP(c) {
		return
	}
	if !strings.Contains(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "MCP requests must use application/json"})
		return
	}
	var req rpcRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "invalid JSON-RPC request"}})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		c.JSON(http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32600, Message: "invalid JSON-RPC request"}})
		return
	}
	protocolVersion := c.GetHeader("MCP-Protocol-Version")
	modern := protocolVersion == modernMCPVersion
	if protocolVersion != "" && protocolVersion != modernMCPVersion && protocolVersion != legacyMCPVersion {
		c.Header("MCP-Protocol-Version", modernMCPVersion)
		c.JSON(http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32022, Message: "unsupported MCP protocol version"}})
		return
	}
	if modern {
		if err := validateModernHeaders(c, req); err != nil {
			c.JSON(http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32020, Message: err.Error()}})
			return
		}
	}
	// Notifications have no id and must not receive a JSON-RPC response.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		if strings.HasPrefix(req.Method, "notifications/") {
			c.Status(http.StatusAccepted)
			return
		}
		c.JSON(http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32600, Message: "request id is required"}})
		return
	}
	var result any
	var rpcErr *rpcError
	switch req.Method {
	case "initialize":
		if modern {
			rpcErr = &rpcError{Code: -32601, Message: "initialize is not supported in the stateless MCP protocol"}
		} else {
			result = gin.H{"protocolVersion": legacyMCPVersion, "capabilities": gin.H{"tools": gin.H{"listChanged": false}}, "serverInfo": gin.H{"name": "knowledge-base", "version": "0.1.0"}, "instructions": "Knowledge base MCP. Use unit_id to scope all records to a specific project."}
		}
	case "server/discover":
		if !modern {
			rpcErr = &rpcError{Code: -32601, Message: "server/discover requires the 2026-07-28 protocol"}
		} else {
			result = gin.H{"resultType": "complete", "supportedVersions": []string{modernMCPVersion}, "capabilities": gin.H{"tools": gin.H{}}, "_meta": gin.H{"io.modelcontextprotocol/serverInfo": gin.H{"name": "knowledge-base", "version": "0.1.0"}}, "instructions": "Use list_units first to identify a unit_id. The archive tools are read-only and all unit-specific queries require unit_id.", "ttlMs": 300000, "cacheScope": "private"}
		}
	case "ping":
		if modern {
			rpcErr = &rpcError{Code: -32601, Message: "method not found: ping"}
		} else {
			result = gin.H{}
		}
	case "tools/list":
		result = gin.H{"tools": s.mcpTools()}
		if modern {
			result.(gin.H)["resultType"] = "complete"
			result.(gin.H)["ttlMs"] = 300000
			result.(gin.H)["cacheScope"] = "private"
		}
	case "tools/call":
		result, rpcErr = s.mcpCall(c, req.Params)
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rpcErr}
	if modern && rpcErr == nil {
		if resultMap, ok := result.(gin.H); ok {
			if _, exists := resultMap["resultType"]; !exists {
				resultMap["resultType"] = "complete"
			}
			if _, exists := resultMap["_meta"]; !exists {
				resultMap["_meta"] = gin.H{"io.modelcontextprotocol/serverInfo": gin.H{"name": "knowledge-base", "version": "0.1.0"}}
			}
		}
	}
	if protocolVersion == "" && req.Method == "initialize" {
		protocolVersion = legacyMCPVersion
	}
	if protocolVersion != "" {
		c.Header("MCP-Protocol-Version", protocolVersion)
	}
	c.JSON(http.StatusOK, response)
}

func validateModernHeaders(c *gin.Context, req rpcRequest) error {
	if method := c.GetHeader("Mcp-Method"); method == "" || method != req.Method {
		return fmt.Errorf("Mcp-Method header must match the JSON-RPC method")
	}
	var params map[string]json.RawMessage
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fmt.Errorf("invalid request params")
		}
	}
	meta := map[string]json.RawMessage{}
	if params != nil && len(params["_meta"]) > 0 {
		_ = json.Unmarshal(params["_meta"], &meta)
	}
	var requestVersion string
	if len(meta["io.modelcontextprotocol/protocolVersion"]) > 0 {
		_ = json.Unmarshal(meta["io.modelcontextprotocol/protocolVersion"], &requestVersion)
	}
	if requestVersion != modernMCPVersion {
		return fmt.Errorf("request _meta protocolVersion must match MCP-Protocol-Version")
	}
	if len(meta["io.modelcontextprotocol/clientCapabilities"]) == 0 {
		return fmt.Errorf("request _meta clientCapabilities is required")
	}
	var capabilities map[string]json.RawMessage
	if err := json.Unmarshal(meta["io.modelcontextprotocol/clientCapabilities"], &capabilities); err != nil || capabilities == nil {
		return fmt.Errorf("request _meta clientCapabilities must be an object")
	}
	if req.Method == "tools/call" {
		var name string
		if len(params["name"]) > 0 {
			_ = json.Unmarshal(params["name"], &name)
		}
		if name == "" || c.GetHeader("Mcp-Name") != name {
			return fmt.Errorf("Mcp-Name header must match tools/call params.name")
		}
	}
	return nil
}

func (s *Server) authorizeMCP(c *gin.Context) bool {
	token := os.Getenv("MCP_AUTH_TOKEN")
	if token == "" {
		logging.App().Error("mcp_disabled", "reason", "MCP_AUTH_TOKEN is not configured")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MCP_AUTH_TOKEN must be configured before enabling the MCP endpoint"})
		return false
	}
	parts := strings.Fields(c.GetHeader("Authorization"))
	provided := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		provided = parts[1]
	}
	if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
		logging.App().Warn("mcp_auth_failed",
			"request_id", c.GetString(requestIDKey),
			"ip", c.ClientIP(),
		)
		c.Header("WWW-Authenticate", "Bearer")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid MCP bearer token"})
		return false
	}
	return true
}

func (s *Server) mcpTools() []gin.H {
	unitID := gin.H{"type": "string", "description": "Unit ID from list_units"}
	readOnly := gin.H{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}
	return []gin.H{
		{"name": "list_units", "description": "List projects available in the archive.", "annotations": readOnly, "inputSchema": gin.H{"type": "object", "properties": gin.H{}, "additionalProperties": false}},
		{"name": "get_unit_profile", "description": "Get a unit overview including document, audit, and open hazard counts.", "annotations": readOnly, "inputSchema": objectSchema(gin.H{"unit_id": unitID}, "unit_id")},
		{"name": "query_hazards", "description": "List hazards for a unit. Optionally filter by status.", "annotations": readOnly, "inputSchema": objectSchema(gin.H{"unit_id": unitID, "status": gin.H{"type": "string", "description": "Optional exact status filter"}}, "unit_id")},
		{"name": "audit_history", "description": "Get audit records for a unit, newest first.", "annotations": readOnly, "inputSchema": objectSchema(gin.H{"unit_id": unitID}, "unit_id")},
		{"name": "vector_search", "description": "Search indexed unit archive documents by semantic similarity and return source excerpts.", "annotations": readOnly, "inputSchema": objectSchema(gin.H{"unit_id": unitID, "query": gin.H{"type": "string", "description": "Question or search phrase"}, "top_k": gin.H{"type": "integer", "minimum": 1, "maximum": 20, "default": 5}}, "unit_id", "query")},
	}
}

func objectSchema(properties gin.H, required ...string) gin.H {
	return gin.H{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func (s *Server) mcpCall(c *gin.Context, raw json.RawMessage) (any, *rpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.Name == "" {
		return nil, &rpcError{Code: -32602, Message: "tools/call requires a tool name"}
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 28*time.Second)
	defer cancel()
	if s.db == nil {
		return toolError("PostgreSQL is not configured"), nil
	}
	args := params.Arguments
	if args == nil {
		args = map[string]any{}
	}
	getString := func(key string) string { value, _ := args[key].(string); return strings.TrimSpace(value) }
	unitID := getString("unit_id")
	var data any
	var err error
	switch params.Name {
	case "list_units":
		data, err = s.listUnits(ctx)
	case "get_unit_profile":
		if unitID == "" {
			return toolError("unit_id is required"), nil
		}
		data, err = s.unitProfile(ctx, unitID)
	case "query_hazards":
		if unitID == "" {
			return toolError("unit_id is required"), nil
		}
		data, err = s.queryHazards(ctx, unitID, getString("status"))
	case "audit_history":
		if unitID == "" {
			return toolError("unit_id is required"), nil
		}
		data, err = s.auditHistory(ctx, unitID)
	case "vector_search":
		query := getString("query")
		if unitID == "" || query == "" {
			return toolError("unit_id and query are required"), nil
		}
		k := 5
		if n, ok := args["top_k"].(float64); ok && n >= 1 && n <= 20 {
			k = int(n)
		}
		data, err = s.vectorSearch(ctx, unitID, query, k)
	default:
		return toolError("unknown tool: " + params.Name), nil
	}
	attrs := []any{
		"request_id", c.GetString(requestIDKey),
		"tool", params.Name,
		"duration_ms", time.Since(started).Milliseconds(),
	}
	if unitID != "" {
		attrs = append(attrs, "unit_id", unitID)
	}
	if err != nil {
		attrs = append(attrs, "error", err)
		logging.App().Warn("mcp_tool_failed", attrs...)
		return toolError(mcpErrorMessage(err)), nil
	}
	logging.App().Info("mcp_tool_called", attrs...)
	return toolResult(data), nil
}

// mcpErrorMessage exposes business errors but keeps dependency details (DB
// hosts, driver output, provider responses) in the server log.
func mcpErrorMessage(err error) string {
	if errors.Is(err, pgx.ErrNoRows) {
		return "not found"
	}
	if errors.Is(err, errEmbeddingConfig) || strings.HasPrefix(err.Error(), "unit not found") {
		return err.Error()
	}
	return "internal error"
}

func toolResult(data any) gin.H {
	b, _ := json.MarshalIndent(data, "", "  ")
	return gin.H{"content": []gin.H{{"type": "text", "text": string(b)}}}
}
func toolError(message string) gin.H {
	return gin.H{"isError": true, "content": []gin.H{{"type": "text", "text": message}}}
}

func (s *Server) listUnits(ctx context.Context) ([]gin.H, error) {
	rows, err := s.db.Query(ctx, `SELECT unit_id,name,unit_type,industry FROM units ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []gin.H{}
	for rows.Next() {
		var id, name, kind, industry string
		if err := rows.Scan(&id, &name, &kind, &industry); err != nil {
			return nil, err
		}
		result = append(result, gin.H{"unit_id": id, "name": name, "unit_type": kind, "industry": industry})
	}
	return result, rows.Err()
}

func (s *Server) unitProfile(ctx context.Context, id string) (gin.H, error) {
	var name, kind, industry string
	if err := s.db.QueryRow(ctx, `SELECT name,unit_type,industry FROM units WHERE unit_id=$1`, id).Scan(&name, &kind, &industry); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("unit not found: %s", id)
		}
		return nil, err
	}
	var audits, hazards, chunks int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM audit_records WHERE unit_id=$1`, id).Scan(&audits); err != nil {
		return nil, err
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM hazards WHERE unit_id=$1 AND status NOT IN ('已整改','已关闭')`, id).Scan(&hazards); err != nil {
		return nil, err
	}
	if s.vectors != nil {
		if docIDs, idErr := s.docIDsForUnits(ctx, []string{id}); idErr == nil {
			chunks, _ = s.vectors.Count(ctx, docIDs)
		}
	}
	var documents int
	var docTypesRaw []byte
	if err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM document_units WHERE unit_id=$1),
		COALESCE((SELECT json_agg(DISTINCT d.doc_type) FILTER (WHERE d.doc_type<>'')
			FROM documents d JOIN document_units du ON du.doc_id=d.doc_id
			WHERE du.unit_id=$1), '[]'::json)`, id).Scan(&documents, &docTypesRaw); err != nil {
		return nil, err
	}
	docTypes := []string{}
	_ = json.Unmarshal(docTypesRaw, &docTypes)
	return gin.H{
		"unit_id": id, "name": name, "unit_type": kind, "industry": industry,
		"audit_count": audits, "open_hazards": hazards, "indexed_chunks": chunks,
		"document_count": documents, "doc_types": docTypes,
	}, nil
}

func (s *Server) queryHazards(ctx context.Context, id, status string) ([]gin.H, error) {
	query := `SELECT id,hazard,status,severity,source,occurred_at FROM hazards WHERE unit_id=$1`
	args := []any{id}
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
		var rowID int64
		var hazard, state, severity, source string
		var at any
		if err := rows.Scan(&rowID, &hazard, &state, &severity, &source, &at); err != nil {
			return nil, err
		}
		result = append(result, gin.H{"id": rowID, "hazard": hazard, "status": state, "severity": severity, "source": source, "occurred_at": at})
	}
	return result, rows.Err()
}

func (s *Server) auditHistory(ctx context.Context, id string) ([]gin.H, error) {
	rows, err := s.db.Query(ctx, `SELECT id,title,score,risk_level,payload,created_at FROM audit_records WHERE unit_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []gin.H{}
	for rows.Next() {
		var rowID int64
		var title, risk string
		var score *float64
		var payload []byte
		var created any
		if err := rows.Scan(&rowID, &title, &score, &risk, &payload, &created); err != nil {
			return nil, err
		}
		var decoded any
		_ = json.Unmarshal(payload, &decoded)
		result = append(result, gin.H{"id": rowID, "title": title, "score": score, "risk_level": risk, "payload": decoded, "created_at": created})
	}
	return result, rows.Err()
}

// searchChunks embeds a query and retrieves unit-scoped chunks. It is shared by
// the MCP vector_search tool and the internal agent.
func (s *Server) searchChunks(ctx context.Context, unitID, query string, k int) ([]vectorstore.Match, error) {
	if s.vectors == nil {
		return nil, fmt.Errorf("vector store is not configured")
	}
	vectors, err := s.embedContext(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, fmt.Errorf("embedding API returned no vectors")
	}
	docIDs, err := s.docIDsForUnits(ctx, []string{unitID})
	if err != nil {
		return nil, err
	}
	return s.vectors.Search(ctx, docIDs, vectors[0], k)
}

func (s *Server) vectorSearch(ctx context.Context, unitID, query string, k int) ([]gin.H, error) {
	matches, err := s.searchChunks(ctx, unitID, query, k)
	if err != nil {
		return nil, err
	}
	result := make([]gin.H, 0, len(matches))
	for _, match := range matches {
		result = append(result, gin.H{"filename": match.Filename, "doc_type": match.DocType, "content": match.Content, "distance": match.Distance})
	}
	return result, nil
}
