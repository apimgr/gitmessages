package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	graphqlapi "github.com/apimgr/gitmessages/src/graphql"
	"github.com/apimgr/gitmessages/src/mode"
)

const mcpProtocolVersion = "2026-07-28"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params"`
}

type mcpError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
	Meta    map[string]any  `json:"_meta,omitempty"`
}

var (
	serverFQDNMu sync.RWMutex
	serverFQDN   string
)

type apiOperation struct {
	name        string
	path        string
	method      string
	summary     string
	description string
	handler     http.HandlerFunc
	inputSchema map[string]any
	Auth        string
	Listed      bool
	Internal    bool
	DebugOnly   bool
	Request     any
	Response    any
	Primary     string
	Message     string
	Binary      string
}

func apiOperationMounted(definition apiOperation, debugEnabled bool) bool {
	return !definition.DebugOnly || debugEnabled
}

func apiOperationVisible(definition apiOperation, debugEnabled bool) bool {
	return apiOperationMounted(definition, debugEnabled) && definition.Listed && !definition.Internal
}

func apiOperationVisibleNow(definition apiOperation) bool {
	return apiOperationVisible(definition, mode.IsDebugEnabled())
}

// SetServerFQDN sets the public hostname used by MCP's origin check.
func SetServerFQDN(host string) {
	serverFQDNMu.Lock()
	defer serverFQDNMu.Unlock()
	serverFQDN = strings.TrimSpace(host)
}

func mcpHandler(w http.ResponseWriter, r *http.Request) {
	if !mcpOriginAllowed(r) {
		writeMCPError(w, http.StatusForbidden, nil, -32600, "Origin is not allowed", nil)
		return
	}
	store := currentAuthStore()
	if store == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="gitmessages"`)
		writeMCPError(w, http.StatusUnauthorized, nil, -32000, "Authentication required", nil)
		return
	}
	identity, authErr := bearerIdentity(r.Context(), r, store, false)
	if authErr != nil {
		if !TokenAuthFailure(w, r) {
			return
		}
		challenge := `Bearer realm="gitmessages"`
		if strings.Contains(strings.ToLower(r.Header.Get("Authorization")), "bearer ") {
			challenge += `, error="invalid_token"`
		}
		w.Header().Set("WWW-Authenticate", challenge)
		writeMCPError(w, http.StatusUnauthorized, nil, -32000, "Invalid or missing bearer token", nil)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), mcpIdentityKey{}, identity))
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeMCPError(w, http.StatusMethodNotAllowed, nil, -32600, "POST is required", nil)
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeMCPError(w, http.StatusUnsupportedMediaType, nil, -32600, "Content-Type must be application/json", nil)
		return
	}
	if !acceptsMCP(r.Header.Get("Accept")) {
		writeMCPError(w, http.StatusNotAcceptable, nil, -32600, "Accept must include application/json and text/event-stream", nil)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeMCPError(w, http.StatusBadRequest, nil, -32700, "Invalid JSON-RPC body", nil)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request mcpRequest
	if err := decoder.Decode(&request); err != nil {
		writeMCPError(w, http.StatusBadRequest, nil, -32700, "Invalid JSON-RPC body", nil)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeMCPError(w, http.StatusBadRequest, request.ID, -32600, "A request must contain one JSON object", nil)
		return
	}
	if request.JSONRPC != "2.0" || !validMCPID(request.ID) || request.Method == "" {
		writeMCPError(w, http.StatusBadRequest, request.ID, -32600, "Invalid JSON-RPC request", nil)
		return
	}
	version := ""
	if metadata, ok := request.Params["_meta"].(map[string]any); ok {
		version, _ = metadata["io.modelcontextprotocol/protocolVersion"].(string)
	}
	headerVersion := r.Header.Get("MCP-Protocol-Version")
	if request.Method == "initialize" && version == "" && headerVersion == "" {
		writeMCPError(w, http.StatusBadRequest, request.ID, -32022, "Unsupported protocol version", map[string]any{"supported": []string{mcpProtocolVersion}})
		return
	}
	requestedVersion := headerVersion
	if requestedVersion == "" {
		requestedVersion = version
	}
	if (headerVersion != "" && headerVersion != mcpProtocolVersion) || (version != "" && version != mcpProtocolVersion) {
		writeMCPError(w, http.StatusBadRequest, request.ID, -32022, "Unsupported protocol version", map[string]any{"supported": []string{mcpProtocolVersion}, "requested": requestedVersion})
		return
	}
	if version == "" || headerVersion == "" || headerVersion != version {
		writeMCPError(w, http.StatusBadRequest, request.ID, -32020, "Header mismatch: MCP-Protocol-Version does not match request metadata", nil)
		return
	}
	if r.Header.Get("Mcp-Method") != request.Method {
		writeMCPError(w, http.StatusBadRequest, request.ID, -32020, "Header mismatch: Mcp-Method does not match request method", nil)
		return
	}
	if request.Method == "tools/call" || request.Method == "resources/read" || request.Method == "prompts/get" {
		name, _ := request.Params["name"].(string)
		if request.Method == "resources/read" {
			name, _ = request.Params["uri"].(string)
		}
		if name == "" || decodeMCPName(r.Header.Get("Mcp-Name")) != name {
			writeMCPError(w, http.StatusBadRequest, request.ID, -32020, "Header mismatch: Mcp-Name does not match request parameters", nil)
			return
		}
	}
	result, status, rpcErr := handleMCPRequest(r, request)
	if rpcErr != nil {
		writeMCPError(w, status, request.ID, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result, Meta: mcpResponseMetadata()})
}

type mcpIdentityKey struct{}

func decodeMCPName(value string) string {
	if !strings.HasPrefix(value, "=?base64?") || !strings.HasSuffix(value, "?=") {
		return value
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?=")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return value
	}
	return string(decoded)
}

func acceptsMCP(header string) bool {
	jsonAccepted := false
	eventStreamAccepted := false
	for _, value := range strings.Split(header, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		if rawQuality, ok := parameters["q"]; ok {
			quality, qualityErr := strconv.ParseFloat(rawQuality, 64)
			if qualityErr != nil || quality <= 0 || quality > 1 {
				continue
			}
		}
		if mediaType == "" {
			continue
		}
		switch strings.ToLower(mediaType) {
		case "application/json":
			jsonAccepted = true
		case "text/event-stream":
			eventStreamAccepted = true
		}
	}
	return jsonAccepted && eventStreamAccepted
}

func validMCPID(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch value.(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

func mcpOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if strings.EqualFold(scheme, parsed.Scheme) && strings.EqualFold(r.Host, parsed.Host) {
		return true
	}
	corsOriginsMu.RLock()
	origins := append([]string(nil), corsOrigins...)
	corsOriginsMu.RUnlock()
	for _, allowed := range origins {
		if allowed != "*" && strings.EqualFold(strings.TrimRight(allowed, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	serverFQDNMu.RLock()
	fqdn := strings.TrimSpace(serverFQDN)
	serverFQDNMu.RUnlock()
	if fqdn != "" && strings.EqualFold(parsed.Host, fqdn) {
		return true
	}
	return false
}

func handleMCPRequest(r *http.Request, request mcpRequest) (any, int, *mcpError) {
	switch request.Method {
	case "server/discover":
		return map[string]any{
			"resultType":                "complete",
			"supportedProtocolVersions": []string{mcpProtocolVersion},
			"capabilities":              map[string]any{"tools": map[string]any{}},
			"instructions":              tr(r, "mcp.help.description"),
			"ttlMs":                     60000,
			"cacheScope":                "public",
		}, http.StatusOK, nil
	case "tools/list":
		return map[string]any{"resultType": "complete", "tools": mcpTools(r), "ttlMs": 60000, "cacheScope": "public"}, http.StatusOK, nil
	case "tools/call":
		return callMCPTool(r, request.Params)
	default:
		return nil, http.StatusNotFound, &mcpError{Code: -32601, Message: "Method not found"}
	}
}

func mcpResponseMetadata() map[string]any {
	return map[string]any{"io.modelcontextprotocol/serverInfo": map[string]string{"name": seoBrandingTitle() + " API", "version": serverVersion}}
}

func apiOperationDefinitions(r *http.Request) []apiOperation {
	definitions := []apiOperation{
		{name: "get_api_index", path: APIBasePath(), method: http.MethodGet, summary: tr(r, "api.rest"), description: tr(r, "api.resources"), handler: apiRoot, inputSchema: emptyInputSchema()},
		{name: "list_commit_types", path: APIBasePath() + "/types", method: http.MethodGet, summary: tr(r, "swagger.types.summary"), description: tr(r, "swagger.types.response"), handler: apiTypes, inputSchema: emptyInputSchema()},
		{name: "list_conventions", path: APIBasePath() + "/conventions", method: http.MethodGet, summary: tr(r, "swagger.conventions.summary"), description: tr(r, "swagger.conventions.response"), handler: apiConventions, inputSchema: emptyInputSchema()},
		{name: "list_templates", path: APIBasePath() + "/templates", method: http.MethodGet, summary: tr(r, "swagger.templates.summary"), description: tr(r, "swagger.templates.response"), handler: apiTemplates, inputSchema: emptyInputSchema()},
		{name: "get_random_template", path: APIBasePath() + "/templates/random", method: http.MethodGet, summary: tr(r, "swagger.random_template.summary"), description: tr(r, "swagger.random_template.response"), handler: apiRandomTemplate, inputSchema: emptyInputSchema()},
		{name: "get_random_example", path: APIBasePath() + "/messages/random", method: http.MethodGet, summary: tr(r, "swagger.random_message.summary"), description: tr(r, "swagger.random_message.response"), handler: apiRandom, inputSchema: emptyInputSchema()},
		{name: "build_commit_message", path: APIBasePath() + "/messages", method: http.MethodPost, summary: tr(r, "swagger.build_message.summary"), description: tr(r, "swagger.build_message.response"), handler: apiMessages, inputSchema: buildMessageInputSchema(r)},
		{name: "get_health", path: APIBasePath() + "/server/healthz", method: http.MethodGet, summary: tr(r, "health.title"), description: tr(r, "health.ok"), handler: apiHealth, inputSchema: emptyInputSchema()},
		{name: "get_about", path: APIBasePath() + "/server/about", method: http.MethodGet, summary: tr(r, "about.title"), description: tr(r, "about.body"), handler: apiAbout, inputSchema: emptyInputSchema()},
		{name: "get_help", path: APIBasePath() + "/server/help", method: http.MethodGet, summary: tr(r, "help.title"), description: tr(r, "help.api.docs"), handler: apiHelp, inputSchema: emptyInputSchema()},
		{name: "get_mcp_help", path: APIBasePath() + "/server/help/mcp", method: http.MethodGet, summary: tr(r, "mcp.title"), description: tr(r, "mcp.help.description"), handler: apiMCPHelp, inputSchema: emptyInputSchema()},
		{name: "get_privacy", path: APIBasePath() + "/server/privacy", method: http.MethodGet, summary: tr(r, "privacy.title"), description: tr(r, "privacy.body"), handler: apiPrivacy, inputSchema: emptyInputSchema()},
		{name: "get_terms", path: APIBasePath() + "/server/terms", method: http.MethodGet, summary: tr(r, "terms.title"), description: tr(r, "terms.body"), handler: apiTerms, inputSchema: emptyInputSchema()},
		{name: "get_security", path: APIBasePath() + "/server/security", method: http.MethodGet, summary: tr(r, "security.title"), description: tr(r, "security.body"), handler: apiContactSecurity, inputSchema: emptyInputSchema()},
		{name: "get_security_policy", path: APIBasePath() + "/server/security/policy", method: http.MethodGet, summary: "Security disclosure policy", description: "Public coordinated-disclosure policy", handler: apiSecurityPolicy, inputSchema: emptyInputSchema()},
		{name: "get_security_thanks", path: APIBasePath() + "/server/security/thanks", method: http.MethodGet, summary: "Security acknowledgments", description: "Public researcher acknowledgments", handler: apiSecurityThanks, inputSchema: emptyInputSchema()},
		{name: "get_data_protection_contact", path: APIBasePath() + "/server/dpo", method: http.MethodGet, summary: "Data protection contact", description: "Public DPO contact information", handler: apiDPO, inputSchema: emptyInputSchema()},
		{name: "get_contact_form", path: APIBasePath() + "/server/contact", method: http.MethodGet, summary: "Contact report form", description: "Returns report-form descriptors and a one-use captcha challenge", handler: apiContact, inputSchema: emptyInputSchema()},
		{name: "submit_contact_report", path: APIBasePath() + "/server/contact", method: http.MethodPost, summary: "Submit a contact report", description: "Uses a submit and confirm review flow", handler: apiContact, inputSchema: contactInputSchema()},
		{name: "get_preferences", path: APIBasePath() + "/server/preferences", method: http.MethodGet, summary: tr(r, "preferences.title"), description: tr(r, "preferences.title"), handler: apiPreferences, inputSchema: emptyInputSchema()},
		{name: "save_preferences", path: APIBasePath() + "/server/preferences", method: http.MethodPost, summary: tr(r, "preferences.save"), description: tr(r, "preferences.title"), handler: apiPreferences, inputSchema: map[string]any{"type": "object", "properties": map[string]any{"theme": map[string]any{"type": "string", "enum": []string{"dark", "light", "auto"}}, "lang": map[string]any{"type": "string"}}, "additionalProperties": true}},
		{name: "get_preferences_sync", path: APIBasePath() + "/server/preferences/sync", method: http.MethodGet, summary: tr(r, "preferences.sync"), description: tr(r, "preferences.sync.description"), handler: preferenceSyncAPI, inputSchema: emptyInputSchema()},
		{name: "import_preferences_sync", path: APIBasePath() + "/server/preferences/sync/import", method: http.MethodGet, summary: tr(r, "preferences.sync.import"), description: tr(r, "preferences.sync.description"), handler: preferenceSyncImportAPI, inputSchema: emptyInputSchema()},
		{name: "get_openapi", path: APIBasePath() + "/server/swagger", method: http.MethodGet, summary: tr(r, "api.openapi"), description: tr(r, "api.rest"), handler: swaggerJSON, inputSchema: emptyInputSchema()},
		{name: "graphql_query", path: APIBasePath() + "/server/graphql", method: http.MethodPost, summary: tr(r, "graphql.title"), description: tr(r, "graphql.description"), handler: graphqlapi.Handler().ServeHTTP, inputSchema: graphqlInputSchema(r)},
	}
	for index := range definitions {
		if definitions[index].Auth == "" {
			definitions[index].Auth = "none"
		}
		if !definitions[index].Internal {
			definitions[index].Listed = true
		}
	}
	return definitions
}

func emptyInputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false}
}

func buildMessageInputSchema(r *http.Request) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":        map[string]string{"type": "string", "description": tr(r, "builder.type")},
			"scope":       map[string]string{"type": "string", "description": tr(r, "builder.scope")},
			"description": map[string]string{"type": "string", "description": tr(r, "builder.description")},
			"body":        map[string]string{"type": "string", "description": tr(r, "builder.body")},
			"breaking":    map[string]string{"type": "boolean", "description": tr(r, "builder.breaking")},
			"convention":  map[string]any{"type": "string", "enum": []string{"conventional-commits", "angular"}, "description": tr(r, "builder.convention")},
		},
		"required": []string{"type", "description"},
	}
}

func graphqlInputSchema(r *http.Request) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":     map[string]string{"type": "string", "description": tr(r, "graphql.query")},
			"variables": map[string]any{"type": "object", "additionalProperties": true},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	}
}

func mcpTools(r *http.Request) []map[string]any {
	definitions := apiOperationDefinitions(r)
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		if !apiOperationVisibleNow(definition) {
			continue
		}
		annotations := map[string]bool{"readOnlyHint": definition.method == http.MethodGet}
		if definition.method == http.MethodDelete {
			annotations["destructiveHint"] = true
		}
		if definition.method == http.MethodDelete || definition.method == http.MethodPut {
			annotations["idempotentHint"] = true
		}
		tools = append(tools, map[string]any{
			"name":        definition.name,
			"title":       definition.summary,
			"description": definition.description,
			"inputSchema": definition.inputSchema,
			"annotations": annotations,
		})
	}
	return tools
}

func callMCPTool(r *http.Request, params map[string]any) (any, int, *mcpError) {
	name, _ := params["name"].(string)
	arguments, _ := params["arguments"].(map[string]any)
	var definition *apiOperation
	for _, candidate := range apiOperationDefinitions(r) {
		if apiOperationVisibleNow(candidate) && candidate.name == name {
			definition = &candidate
			break
		}
	}
	if definition == nil {
		return mcpToolError(tr(r, "api.invalid_request")), http.StatusOK, nil
	}
	if definition.method == http.MethodGet && len(arguments) > 0 || definition.name == "build_commit_message" && !validBuildArguments(arguments) || definition.name == "graphql_query" && !validGraphQLArguments(arguments) {
		return mcpToolError(tr(r, "api.invalid_request")), http.StatusOK, nil
	}
	request := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = definition.path
	urlCopy.RawPath = ""
	urlCopy.RawQuery = ""
	request.URL = &urlCopy
	request.Method = definition.method
	request.Header = r.Header.Clone()
	request.Header.Del("Cookie")
	request.Header.Set("Accept", "application/json")
	if definition.method == http.MethodPost {
		body, err := json.Marshal(arguments)
		if err != nil {
			return mcpToolError(tr(r, "api.invalid_request")), http.StatusOK, nil
		}
		request.Header.Set("Content-Type", "application/json")
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	recorder := httptest.NewRecorder()
	definition.handler(recorder, request)
	if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
		return mcpToolError(strings.TrimSpace(recorder.Body.String())), http.StatusOK, nil
	}
	var envelope map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		return nil, http.StatusInternalServerError, &mcpError{Code: -32603, Message: "Unable to decode API response"}
	}
	if ok, exists := envelope["ok"].(bool); exists && !ok {
		return mcpToolError(strings.TrimSpace(recorder.Body.String())), http.StatusOK, nil
	}
	if graphQLErrors, exists := envelope["errors"]; exists {
		encoded, err := json.Marshal(graphQLErrors)
		if err != nil {
			return nil, http.StatusInternalServerError, &mcpError{Code: -32603, Message: "Unable to encode GraphQL errors"}
		}
		return mcpToolError(string(encoded)), http.StatusOK, nil
	}
	data, enveloped := envelope["data"]
	if !enveloped {
		data = envelope
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, http.StatusInternalServerError, &mcpError{Code: -32603, Message: "Unable to encode tool result"}
	}
	return map[string]any{"resultType": "complete", "content": []map[string]string{{"type": "text", "text": string(encoded)}}, "structuredContent": data, "isError": false}, http.StatusOK, nil
}

func validGraphQLArguments(arguments map[string]any) bool {
	if arguments == nil {
		return false
	}
	if _, ok := arguments["query"].(string); !ok {
		return false
	}
	if variables, exists := arguments["variables"]; exists {
		if _, ok := variables.(map[string]any); !ok {
			return false
		}
	}
	for key := range arguments {
		if key != "query" && key != "variables" {
			return false
		}
	}
	return true
}

func validBuildArguments(arguments map[string]any) bool {
	if arguments == nil {
		return false
	}
	for key, value := range arguments {
		switch key {
		case "type", "scope", "description", "body", "convention":
			if _, ok := value.(string); !ok {
				return false
			}
		case "breaking":
			if _, ok := value.(bool); !ok {
				return false
			}
		default:
			return false
		}
	}
	_, hasType := arguments["type"]
	_, hasDescription := arguments["description"]
	return hasType && hasDescription
}

func mcpToolError(message string) map[string]any {
	return map[string]any{"resultType": "complete", "content": []map[string]string{{"type": "text", "text": message}}, "isError": true}
}

func writeMCPError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string, data map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message, Data: data}, Meta: mcpResponseMetadata()})
}
