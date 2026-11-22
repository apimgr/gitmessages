package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/auth"
)

func TestMCPRequiresBearerAndDoesNotLeakToken(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store, err := auth.Open(t.TempDir()+"/server.db", "adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", key, 25, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ConfigureAuth(store)
	defer ConfigureAuth(nil)
	raw, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	requestBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	request := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(requestBody))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
		r.Header.Set("Mcp-Method", "tools/list")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		mcpHandler(w, r)
		return w
	}
	unauthenticated := request("")
	if unauthenticated.Code != http.StatusUnauthorized || unauthenticated.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated MCP returned %d", unauthenticated.Code)
	}
	response := request(raw)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated tools/list returned %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), raw) {
		t.Fatal("tools/list exposed the bearer token")
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	meta, ok := envelope["_meta"].(map[string]any)
	if !ok || meta["io.modelcontextprotocol/serverInfo"] == nil {
		t.Fatalf("tools/list omitted server metadata: %#v", envelope["_meta"])
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result missing: %#v", envelope["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list did not include tools: %#v", result["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] == nil || tool["description"] == nil || tool["inputSchema"] == nil || tool["annotations"] == nil {
		t.Fatalf("tool metadata incomplete: %#v", tool)
	}
	callBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"name":"build_commit_message","arguments":{"type":"feat","description":"MCP auth regression"}}}`
	callRequest := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(callBody))
	callRequest.Header.Set("Content-Type", "application/json")
	callRequest.Header.Set("Accept", "application/json, text/event-stream")
	callRequest.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	callRequest.Header.Set("Mcp-Method", "tools/call")
	callRequest.Header.Set("Mcp-Name", "build_commit_message")
	callRequest.Header.Set("Authorization", "Bearer "+raw)
	callResponse := httptest.NewRecorder()
	mcpHandler(callResponse, callRequest)
	if callResponse.Code != http.StatusOK {
		t.Fatalf("tools/call returned %d: %s", callResponse.Code, callResponse.Body.String())
	}
	if strings.Contains(callResponse.Body.String(), raw) {
		t.Fatal("tools/call exposed the bearer token")
	}
	var callEnvelope map[string]any
	if err := json.Unmarshal(callResponse.Body.Bytes(), &callEnvelope); err != nil {
		t.Fatal(err)
	}
	if callEnvelope["_meta"] == nil || callEnvelope["result"] == nil {
		t.Fatalf("tools/call omitted response metadata or result: %#v", callEnvelope)
	}
}

func TestMCPNameSentinelDecoding(t *testing.T) {
	if got := decodeMCPName("=?base64?Y2Fmw6k=?="); got != "café" {
		t.Fatalf("decoded MCP name %q", got)
	}
	if got := decodeMCPName("plain-name"); got != "plain-name" {
		t.Fatalf("plain MCP name changed to %q", got)
	}
}

func TestLinkedTokenCannotManageTokens(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store, err := auth.Open(t.TempDir()+"/server.db", "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV", key, 25, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ConfigureAuth(store)
	defer ConfigureAuth(nil)
	_, identity, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	linked, _, err := store.CreateLinked(t.Context(), identity.OwnerID, "automation", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, apiBase+"/server/preferences/tokens", strings.NewReader(`{"name":"another","expiry":"never"}`))
	r.Header.Set("Authorization", "Bearer "+linked)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	tokenAPI(w, r)
	if w.Code != http.StatusForbidden || w.Header().Get("WWW-Authenticate") != `Bearer error="insufficient_scope"` {
		t.Fatalf("linked token management returned %d with %q: %s", w.Code, w.Header().Get("WWW-Authenticate"), w.Body.String())
	}
}

func TestLegacyMCPInitializeReturnsUnsupportedProtocolVersion(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	store, err := auth.Open(t.TempDir()+"/server.db", "adm_0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", key, 25, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ConfigureAuth(store)
	defer ConfigureAuth(nil)
	token, _, err := store.IssueOwner(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mcpHandler(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":-32022`) || !strings.Contains(w.Body.String(), `"supported":["2026-07-28"]`) {
		t.Fatalf("legacy initialize response changed: %d %s", w.Code, w.Body.String())
	}
}
