package app

import (
	"encoding/json"
	"net/http"
	"testing"
)

const testMCPToken = "test-token"

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  map[string]any  `json:"result"`
	Error   *rpcError       `json:"error"`
}

func decodeEnvelope(t *testing.T, body []byte) rpcEnvelope {
	t.Helper()
	var out rpcEnvelope
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode MCP response: %v (%s)", err, body)
	}
	return out
}

func mcpPost(t *testing.T, body string, headers map[string]string) *httpResult {
	t.Helper()
	t.Setenv("MCP_AUTH_TOKEN", testMCPToken)
	h := map[string]string{"Authorization": "Bearer " + testMCPToken}
	for k, v := range headers {
		h[k] = v
	}
	w := doRequest(testRouter(testServer()), http.MethodPost, "/mcp", body, h)
	return &httpResult{code: w.Code, body: w.Body.Bytes(), header: w.Result().Header}
}

type httpResult struct {
	code   int
	body   []byte
	header http.Header
}

func TestMCPRequiresConfiguredToken(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", "")
	w := doRequest(testRouter(testServer()), http.MethodPost, "/mcp",
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`, map[string]string{"Authorization": "Bearer anything"})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestMCPRejectsMissingAndWrongToken(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", testMCPToken)
	r := testRouter(testServer())
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`

	w := doRequest(r, http.MethodPost, "/mcp", body, nil)
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("missing token: status = %d, headers = %v", w.Code, w.Header())
	}

	w = doRequest(r, http.MethodPost, "/mcp", body, map[string]string{"Authorization": "Bearer wrong"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", w.Code)
	}
}

func TestMCPRequiresJSONContentType(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", testMCPToken)
	req := doRequest(testRouter(testServer()), http.MethodPost, "/mcp", "", map[string]string{"Authorization": "Bearer " + testMCPToken})
	if req.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", req.Code)
	}
}

func TestMCPLegacyInitializeAndToolsList(t *testing.T) {
	res := mcpPost(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, nil)
	if res.code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", res.code, res.body)
	}
	env := decodeEnvelope(t, res.body)
	if env.Error != nil {
		t.Fatalf("initialize error: %+v", env.Error)
	}
	if got := env.Result["protocolVersion"]; got != legacyMCPVersion {
		t.Fatalf("protocolVersion = %v, want %s", got, legacyMCPVersion)
	}
	if res.header.Get("MCP-Protocol-Version") != legacyMCPVersion {
		t.Fatalf("response protocol header = %q", res.header.Get("MCP-Protocol-Version"))
	}

	res = mcpPost(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, nil)
	env = decodeEnvelope(t, res.body)
	tools, _ := env.Result["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("tools/list returned %d tools: %s", len(tools), res.body)
	}
	names := map[string]bool{}
	for _, item := range tools {
		if tool, ok := item.(map[string]any); ok {
			if name, ok := tool["name"].(string); ok {
				names[name] = true
			}
		}
	}
	for _, want := range []string{"list_units", "get_unit_profile", "query_hazards", "audit_history", "vector_search"} {
		if !names[want] {
			t.Fatalf("tools/list is missing %s: %s", want, res.body)
		}
	}
}

func TestMCPModernProtocolValidation(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + modernMCPVersion + `","io.modelcontextprotocol/clientCapabilities":{}}}}`

	res := mcpPost(t, body, map[string]string{"MCP-Protocol-Version": modernMCPVersion, "Mcp-Method": "tools/list"})
	if res.code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.code, res.body)
	}
	env := decodeEnvelope(t, res.body)
	if env.Result["resultType"] != "complete" {
		t.Fatalf("modern response missing resultType: %s", res.body)
	}
	if _, ok := env.Result["_meta"]; !ok {
		t.Fatalf("modern response missing _meta: %s", res.body)
	}

	res = mcpPost(t, body, map[string]string{"MCP-Protocol-Version": modernMCPVersion})
	env = decodeEnvelope(t, res.body)
	if res.code != http.StatusBadRequest || env.Error == nil || env.Error.Code != -32020 {
		t.Fatalf("missing Mcp-Method: status = %d, error = %+v", res.code, env.Error)
	}

	res = mcpPost(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "1900-01-01"})
	env = decodeEnvelope(t, res.body)
	if res.code != http.StatusBadRequest || env.Error == nil || env.Error.Code != -32022 {
		t.Fatalf("unsupported version: status = %d, error = %+v", res.code, env.Error)
	}
}

func TestMCPNotificationsReturnAccepted(t *testing.T) {
	res := mcpPost(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	if res.code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", res.code)
	}
	if len(res.body) != 0 {
		t.Fatalf("notification returned a body: %s", res.body)
	}
}

func TestMCPToolsCallWithoutDatabase(t *testing.T) {
	res := mcpPost(t, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"list_units","arguments":{}}}`, nil)
	if res.code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.code, res.body)
	}
	env := decodeEnvelope(t, res.body)
	if env.Result["isError"] != true {
		t.Fatalf("expected tool error without database: %s", res.body)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	res := mcpPost(t, `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`, nil)
	env := decodeEnvelope(t, res.body)
	if env.Error == nil || env.Error.Code != -32601 {
		t.Fatalf("error = %+v, want -32601", env.Error)
	}
}

func TestMCPGetMethodNotAllowed(t *testing.T) {
	t.Setenv("MCP_AUTH_TOKEN", testMCPToken)
	w := doRequest(testRouter(testServer()), http.MethodGet, "/mcp", "", nil)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
		t.Fatalf("status = %d, allow = %q", w.Code, w.Header().Get("Allow"))
	}
}
