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

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/mode"
	swaggerapi "github.com/apimgr/gitmessages/src/swagger"
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

type Route struct {
	Method       string
	Path         string
	OperationID  string
	Summary      string
	Description  string
	Handler      http.HandlerFunc
	Auth         string
	Listed       bool
	Internal     bool
	DebugOnly    bool
	MountedWhen  string
	Request      any
	Response     any
	BareResponse bool
	Primary      string
	Message      string
	Binary       string
}

type apiOperation = Route

func apiOperationMounted(definition apiOperation, debugEnabled bool) bool {
	return !definition.DebugOnly || debugEnabled
}

func apiOperationVisible(definition apiOperation, debugEnabled bool) bool {
	return apiOperationListed(definition, debugEnabled) && !isCookieBoundPreferencesRoute(definition) && !strings.HasPrefix(definition.OperationID, "mcpEndpoint")
}

func apiOperationListed(definition apiOperation, debugEnabled bool) bool {
	return apiOperationMounted(definition, debugEnabled) && definition.Listed && !definition.Internal
}

func isCookieBoundPreferencesRoute(definition apiOperation) bool {
	return strings.HasPrefix(definition.Path, APIBasePath()+"/server/preferences")
}

func apiOperationVisibleNow(definition apiOperation) bool {
	return apiOperationVisible(definition, mode.IsDebugEnabled())
}

func apiOperationListedNow(definition apiOperation) bool {
	return apiOperationListed(definition, mode.IsDebugEnabled())
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
	if request.Method == "tools/call" && identity.Scope == "read" {
		toolName, _ := request.Params["name"].(string)
		for _, operation := range apiOperationDefinitions(r) {
			if operation.OperationID == toolName && !mcpOperationAllowedForScope(operation, identity.Scope) {
				w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
				writeMCPError(w, http.StatusForbidden, request.ID, -32003, "Token scope does not permit this operation", nil)
				return
			}
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
		{OperationID: "getApiIndex", Path: APIBasePath(), Method: http.MethodGet, Summary: tr(r, "api.rest"), Description: tr(r, "api.resources"), Handler: apiRoot, Request: emptyInputSchema()},
		{OperationID: "listCommitTypes", Path: APIBasePath() + "/types", Method: http.MethodGet, Summary: tr(r, "swagger.types.summary"), Description: tr(r, "swagger.types.response"), Handler: apiTypes, Request: emptyInputSchema()},
		{OperationID: "listConventions", Path: APIBasePath() + "/conventions", Method: http.MethodGet, Summary: tr(r, "swagger.conventions.summary"), Description: tr(r, "swagger.conventions.response"), Handler: apiConventions, Request: emptyInputSchema()},
		{OperationID: "listTemplates", Path: APIBasePath() + "/templates", Method: http.MethodGet, Summary: tr(r, "swagger.templates.summary"), Description: tr(r, "swagger.templates.response"), Handler: apiTemplates, Request: emptyInputSchema()},
		{OperationID: "getRandomTemplate", Path: APIBasePath() + "/templates/random", Method: http.MethodGet, Summary: tr(r, "swagger.random_template.summary"), Description: tr(r, "swagger.random_template.response"), Handler: apiRandomTemplate, Request: emptyInputSchema()},
		{OperationID: "getRandomExample", Path: APIBasePath() + "/messages/random", Method: http.MethodGet, Summary: tr(r, "swagger.random_message.summary"), Description: tr(r, "swagger.random_message.response"), Handler: apiRandom, Request: emptyInputSchema()},
		{OperationID: "getMessageBuilderForm", Path: APIBasePath() + "/messages", Method: http.MethodGet, Summary: tr(r, "builder.title"), Description: tr(r, "builder.description"), Handler: apiMessageForm, Request: emptyInputSchema()},
		{OperationID: "buildCommitMessage", Path: APIBasePath() + "/messages", Method: http.MethodPost, Summary: tr(r, "swagger.build_message.summary"), Description: tr(r, "swagger.build_message.response"), Handler: apiMessages, Request: buildMessageInputSchema(r), Primary: "message"},
		{OperationID: "getHealth", Path: APIBasePath() + "/server/healthz", Method: http.MethodGet, Summary: tr(r, "health.title"), Description: tr(r, "health.ok"), Handler: apiHealth, Request: emptyInputSchema()},
		{OperationID: "getAbout", Path: APIBasePath() + "/server/about", Method: http.MethodGet, Summary: tr(r, "about.title"), Description: tr(r, "about.body"), Handler: apiAbout, Request: emptyInputSchema()},
		{OperationID: "getHelp", Path: APIBasePath() + "/server/help", Method: http.MethodGet, Summary: tr(r, "help.title"), Description: tr(r, "help.api.docs"), Handler: apiHelp, Request: emptyInputSchema()},
		{OperationID: "getMcpHelp", Path: APIBasePath() + "/server/help/mcp", Method: http.MethodGet, Summary: tr(r, "mcp.title"), Description: tr(r, "mcp.help.description"), Handler: apiMCPHelp, Request: emptyInputSchema()},
		{OperationID: "getPrivacy", Path: APIBasePath() + "/server/privacy", Method: http.MethodGet, Summary: tr(r, "privacy.title"), Description: tr(r, "privacy.body"), Handler: apiPrivacy, Request: emptyInputSchema()},
		{OperationID: "getTerms", Path: APIBasePath() + "/server/terms", Method: http.MethodGet, Summary: tr(r, "terms.title"), Description: tr(r, "terms.body"), Handler: apiTerms, Request: emptyInputSchema()},
		{OperationID: "getSecurity", Path: APIBasePath() + "/server/security", Method: http.MethodGet, Summary: tr(r, "security.title"), Description: tr(r, "security.body"), Handler: apiContactSecurity, Request: emptyInputSchema()},
		{OperationID: "getSecurityPolicy", Path: APIBasePath() + "/server/security/policy", Method: http.MethodGet, Summary: "Security disclosure policy", Description: "Public coordinated-disclosure policy", Handler: apiSecurityPolicy, Request: emptyInputSchema()},
		{OperationID: "getSecurityThanks", Path: APIBasePath() + "/server/security/thanks", Method: http.MethodGet, Summary: "Security acknowledgments", Description: "Public researcher acknowledgments", Handler: apiSecurityThanks, Request: emptyInputSchema()},
		{OperationID: "getDataProtectionContact", Path: APIBasePath() + "/server/dpo", Method: http.MethodGet, Summary: "Data protection contact", Description: "Public DPO contact information", Handler: apiDPO, Request: emptyInputSchema()},
		{OperationID: "getContactForm", Path: APIBasePath() + "/server/contact", Method: http.MethodGet, Summary: "Contact report form", Description: "Returns report-form descriptors and a one-use captcha challenge", Handler: apiContact, Request: emptyInputSchema()},
		{OperationID: "submitContactReport", Path: APIBasePath() + "/server/contact", Method: http.MethodPost, Summary: "Submit a contact report", Description: "Uses a submit and confirm review flow", Handler: apiContact, Request: contactInputSchema()},
		{OperationID: "getPreferences", Path: APIBasePath() + "/server/preferences", Method: http.MethodGet, Summary: tr(r, "preferences.title"), Description: tr(r, "preferences.title"), Handler: apiPreferences, Request: emptyInputSchema()},
		{OperationID: "savePreferences", Path: APIBasePath() + "/server/preferences", Method: http.MethodPost, Summary: tr(r, "preferences.save"), Description: tr(r, "preferences.title"), Handler: apiPreferences, Request: map[string]any{"type": "object", "properties": map[string]any{"theme": map[string]any{"type": "string", "enum": []string{"dark", "light", "auto"}}, "lang": map[string]any{"type": "string"}}, "additionalProperties": true}},
		{OperationID: "getPreferencesSync", Path: APIBasePath() + "/server/preferences/sync", Method: http.MethodGet, Summary: tr(r, "preferences.sync"), Description: tr(r, "preferences.sync.description"), Handler: preferenceSyncAPI, Request: emptyInputSchema()},
		{OperationID: "importPreferencesSync", Path: APIBasePath() + "/server/preferences/sync/import", Method: http.MethodGet, Summary: tr(r, "preferences.sync.import"), Description: tr(r, "preferences.sync.description"), Handler: preferenceSyncImportAPI, Request: emptyInputSchema()},
		{OperationID: "getOpenAPI", Path: APIBasePath() + "/server/swagger", Method: http.MethodGet, Summary: tr(r, "api.openapi"), Description: tr(r, "api.rest"), Handler: swaggerJSON, Request: emptyInputSchema()},
		{OperationID: "graphqlQuery", Path: APIBasePath() + "/server/graphql", Method: http.MethodPost, Summary: tr(r, "graphql.title"), Description: tr(r, "graphql.description"), Handler: graphQLHandler, Request: graphqlInputSchema(r)},
		{OperationID: "getApiAutodiscover", Path: "/api/autodiscover", Method: http.MethodGet, Summary: tr(r, "api.rest"), Description: tr(r, "api.resources"), Handler: apiAutodiscover, Request: emptyInputSchema()},
		{OperationID: "getApiHealthz", Path: "/api/healthz", Method: http.MethodGet, Summary: tr(r, "health.title"), Description: tr(r, "health.ok"), Handler: apiHealth, Request: emptyInputSchema()},
		{OperationID: "getApiSwagger", Path: "/api/swagger", Method: http.MethodGet, Summary: tr(r, "api.openapi"), Description: tr(r, "api.rest"), Handler: swaggerJSON, Request: emptyInputSchema()},
		{OperationID: "graphqlQueryApiGraphql", Path: "/api/graphql", Method: http.MethodPost, Summary: tr(r, "graphql.title"), Description: tr(r, "graphql.description"), Handler: graphQLHandler, Request: graphqlInputSchema(r)},
		{OperationID: "mcpEndpoint", Path: APIBasePath() + "/server/mcp", Method: http.MethodPost, Summary: tr(r, "mcp.title"), Description: tr(r, "mcp.help.description"), Handler: mcpHandler, Auth: "token", Request: mcpInputSchema()},
		{OperationID: "mcpEndpointApiMcp", Path: "/api/mcp", Method: http.MethodPost, Summary: tr(r, "mcp.title"), Description: tr(r, "mcp.help.description"), Handler: mcpHandler, Auth: "token", Request: mcpInputSchema()},
		{OperationID: "mcpEndpointMcp", Path: "/mcp", Method: http.MethodPost, Summary: tr(r, "mcp.title"), Description: tr(r, "mcp.help.description"), Handler: mcpHandler, Auth: "token", Request: mcpInputSchema()},
		{OperationID: "listOwnerTokens", Path: APIBasePath() + "/server/preferences/tokens", Method: http.MethodGet, Summary: tr(r, "tokens.title"), Description: tr(r, "tokens.title"), Handler: tokenAPI, Auth: "owner", Request: emptyInputSchema()},
		{OperationID: "createLinkedToken", Path: APIBasePath() + "/server/preferences/tokens", Method: http.MethodPost, Summary: tr(r, "tokens.create_linked"), Description: tr(r, "tokens.access"), Handler: tokenAPI, Auth: "owner", Request: tokenCreateSchema(), Message: "Created successfully"},
		{OperationID: "issuePrimaryToken", Path: APIBasePath() + "/server/preferences/tokens/primary", Method: http.MethodPost, Summary: tr(r, "tokens.new"), Description: tr(r, "tokens.primary"), Handler: tokenAPI, Request: emptyInputSchema(), Message: "Created successfully"},
		{OperationID: "revokePrimaryToken", Path: APIBasePath() + "/server/preferences/tokens/primary", Method: http.MethodDelete, Summary: tr(r, "tokens.revoke_primary"), Description: tr(r, "tokens.primary_revoke_summary"), Handler: tokenAPI, Auth: "owner", Request: tokenConfirmSchema()},
		{OperationID: "revealPrimaryToken", Path: APIBasePath() + "/server/preferences/tokens/primary/reveal", Method: http.MethodPost, Summary: tr(r, "tokens.reveal"), Description: tr(r, "tokens.shown_once"), Handler: tokenAPI, Auth: "owner", Request: emptyInputSchema()},
		{OperationID: "rotatePrimaryToken", Path: APIBasePath() + "/server/preferences/tokens/primary/rotate", Method: http.MethodPost, Summary: tr(r, "tokens.rotate"), Description: tr(r, "tokens.primary"), Handler: tokenAPI, Auth: "owner", Request: emptyInputSchema()},
		{OperationID: "revealLinkedToken", Path: APIBasePath() + "/server/preferences/tokens/{id}/reveal", Method: http.MethodPost, Summary: tr(r, "tokens.reveal"), Description: tr(r, "tokens.shown_once"), Handler: tokenAPI, Auth: "owner", Request: emptyInputSchema()},
		{OperationID: "rotateLinkedToken", Path: APIBasePath() + "/server/preferences/tokens/{id}/rotate", Method: http.MethodPost, Summary: tr(r, "tokens.rotate"), Description: tr(r, "tokens.primary"), Handler: tokenAPI, Auth: "owner", Request: emptyInputSchema()},
		{OperationID: "updateLinkedToken", Path: APIBasePath() + "/server/preferences/tokens/{id}", Method: http.MethodPatch, Summary: tr(r, "tokens.edit"), Description: tr(r, "tokens.save"), Handler: tokenAPI, Auth: "owner", Request: tokenNameExpirySchema()},
		{OperationID: "revokeLinkedToken", Path: APIBasePath() + "/server/preferences/tokens/{id}", Method: http.MethodDelete, Summary: tr(r, "tokens.revoke"), Description: tr(r, "tokens.revoke"), Handler: tokenAPI, Auth: "owner", Request: emptyInputSchema()},
	}
	for index := range definitions {
		if definitions[index].Auth == "" {
			definitions[index].Auth = "none"
		}
		if !definitions[index].Internal {
			definitions[index].Listed = true
		}
		definitions[index].Response = apiOperationResponseModel(definitions[index].OperationID)
		definitions[index].BareResponse = apiOperationBareResponse(definitions[index].OperationID)
	}
	definitions = append(definitions, configuredMetricsRouteDefinitions(r)...)
	return definitions
}

func tokenNameExpirySchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 64}, "expires": map[string]any{"type": "string", "enum": []string{"never", "7d", "30d", "90d", "1y"}}, "expires_on": map[string]any{"type": "string", "format": "date"}}, "not": map[string]any{"required": []string{"expires", "expires_on"}}, "additionalProperties": false}
}

func tokenCreateSchema() map[string]any {
	schema := tokenNameExpirySchema()
	properties := schema["properties"].(map[string]any)
	properties["scope"] = map[string]any{"type": "string", "enum": []string{"read", "read-write"}}
	schema["required"] = []string{"name"}
	return schema
}

func tokenConfirmSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"confirm"}, "properties": map[string]any{"confirm": map[string]any{"type": "string", "minLength": 4, "maxLength": 4}}, "additionalProperties": false}
}

func mcpInputSchema() map[string]any {
	return map[string]any{"type": "object", "required": []string{"jsonrpc", "id", "method", "params"}, "properties": map[string]any{"jsonrpc": map[string]any{"type": "string", "enum": []string{"2.0"}}, "id": map[string]any{"oneOf": []map[string]string{{"type": "string"}, {"type": "number"}}}, "method": map[string]any{"type": "string"}, "params": map[string]any{"type": "object"}}, "additionalProperties": false}
}

func routeRequestSchema(request any) map[string]any {
	if schema, ok := request.(map[string]any); ok {
		return schema
	}
	return swaggerapi.SchemaFromModel(request)
}

func registerAPIRoutes(mux *routeMux) {
	request := &http.Request{Header: make(http.Header)}
	registry := &routeRegistry{mux: mux}
	for _, route := range apiOperationDefinitions(request) {
		if route.MountedWhen != "" && !apiRouteConditionEnabled(route.MountedWhen) {
			continue
		}
		registry.Add(route)
	}
}

func apiRouteConditionEnabled(condition string) bool {
	metricsConfig := config.Get().Server.Metrics
	switch condition {
	case "server.metrics.enabled":
		return metricsConfig.Enabled
	case "server.metrics.root.enabled":
		return metricsConfig.Enabled && metricsConfig.Root.Enabled
	default:
		return false
	}
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
	identity, hasIdentity := r.Context().Value(mcpIdentityKey{}).(auth.Identity)
	tools := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		if !apiOperationVisibleNow(definition) {
			continue
		}
		if hasIdentity && !mcpOperationAllowedForScope(definition, identity.Scope) {
			continue
		}
		annotations := map[string]bool{"readOnlyHint": definition.Method == http.MethodGet}
		if definition.Method == http.MethodDelete {
			annotations["destructiveHint"] = true
		}
		if definition.Method == http.MethodDelete || definition.Method == http.MethodPut {
			annotations["idempotentHint"] = true
		}
		tools = append(tools, map[string]any{
			"name":        definition.OperationID,
			"title":       definition.Summary,
			"description": definition.Description,
			"inputSchema": routeRequestSchema(definition.Request),
			"annotations": annotations,
		})
	}
	return tools
}

func mcpOperationAllowedForScope(operation apiOperation, scope string) bool {
	if scope != "read" || operation.Method == http.MethodGet || operation.Method == http.MethodHead || operation.Method == http.MethodOptions {
		return true
	}
	return operation.OperationID == "graphqlQuery" || operation.OperationID == "graphqlQueryApiGraphql"
}

func callMCPTool(r *http.Request, params map[string]any) (any, int, *mcpError) {
	name, _ := params["name"].(string)
	arguments, _ := params["arguments"].(map[string]any)
	var definition *apiOperation
	for _, candidate := range apiOperationDefinitions(r) {
		if apiOperationVisibleNow(candidate) && candidate.OperationID == name {
			definition = &candidate
			break
		}
	}
	if definition == nil {
		return mcpToolError(tr(r, "api.invalid_request")), http.StatusOK, nil
	}
	if definition.Method == http.MethodGet && len(arguments) > 0 || definition.OperationID == "buildCommitMessage" && !validBuildArguments(arguments) || definition.OperationID == "graphqlQuery" && !validGraphQLArguments(arguments) {
		return mcpToolError(tr(r, "api.invalid_request")), http.StatusOK, nil
	}
	request := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = definition.Path
	urlCopy.RawPath = ""
	urlCopy.RawQuery = ""
	request.URL = &urlCopy
	request.Method = definition.Method
	request.Header = r.Header.Clone()
	request.Header.Del("Cookie")
	request.Header.Set("Accept", "application/json")
	if definition.Method == http.MethodPost {
		body, err := json.Marshal(arguments)
		if err != nil {
			return mcpToolError(tr(r, "api.invalid_request")), http.StatusOK, nil
		}
		request.Header.Set("Content-Type", "application/json")
		request.Body = io.NopCloser(bytes.NewReader(body))
	}
	recorder := httptest.NewRecorder()
	definition.Handler(recorder, request)
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
