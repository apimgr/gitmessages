package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/messages"
)

func TestRouteTextSerializerUsesAPIResponseConventions(t *testing.T) {
	tests := []struct {
		name string
		path string
		verb string
		data any
		want []string
	}{
		{
			name: "primary response value",
			path: "/api/v1/messages",
			verb: http.MethodPost,
			data: map[string]string{"message": "feat: add ordered text output"},
			want: []string{"feat: add ordered text output"},
		},
		{
			name: "list of objects follows struct field order",
			path: "/api/v1/types",
			verb: http.MethodGet,
			data: []messages.CommitType{{Type: "feat", Description: "feature", Emoji: "✨", Example: "feat: add it", Breaking: false}},
			want: []string{"OK: 1 items found", "type: feat", "description: feature", "emoji: ✨", "example: feat: add it", "breaking: false"},
		},
		{
			name: "object fields and nested maps are stable",
			path: "/api/v1/server/about",
			verb: http.MethodGet,
			data: map[string]any{"z": "last", "stats": map[string]any{"requests_24h": 45}, "a": "first"},
			want: []string{"OK: Retrieved successfully", "a: first", "stats.requests_24h: 45", "z: last"},
		},
		{
			name: "scalar list has a count and one item per line",
			path: "/api/v1",
			verb: http.MethodGet,
			data: []string{"types", "templates"},
			want: []string{"OK: 2 items found", "types\ntemplates"},
		},
		{
			name: "empty list has a count",
			path: "/api/v1/types",
			verb: http.MethodGet,
			data: []messages.CommitType{},
			want: []string{"OK: 0 items found"},
		},
		{
			name: "empty mutation uses its operation message",
			path: "/api/v1/server/preferences/tokens/primary",
			verb: http.MethodDelete,
			data: nil,
			want: []string{"OK: Deleted successfully"},
		},
		{
			name: "created operation uses its declared message",
			path: "/api/v1/server/preferences/tokens/primary",
			verb: http.MethodPost,
			data: map[string]string{"owner_token": "once"},
			want: []string{"OK: Created successfully", "owner_token: once"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.verb, test.path, nil)
			got := serializeRouteText(request, test.data)
			for _, part := range test.want {
				if !strings.Contains(got, part) {
					t.Errorf("serialized response %q does not contain %q", got, part)
				}
			}
		})
	}
}

func TestTextErrorsIncludeCodeAndPreserveHTTPStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/messages", nil)
	request.Header.Set("Accept", "text/plain")
	response := httptest.NewRecorder()
	writeError(response, request, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is not allowed")
	if response.Code != http.StatusMethodNotAllowed || response.Body.String() != "ERROR: METHOD_NOT_ALLOWED: GET is not allowed\n" {
		t.Fatalf("text error = status %d, body %q", response.Code, response.Body.String())
	}
}

func TestRouteTextSerializerFallsBackForUnknownRoute(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/unregistered", nil)
	if got := serializeRouteText(request, map[string]string{"key": "value"}); got != "key: value\n" {
		t.Fatalf("unknown route text = %q", got)
	}
}

func TestRouteTextSerializerMatchesDynamicTokenPaths(t *testing.T) {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/server/preferences/tokens/123", nil)
	route := routeForRequest(request)
	if route == nil || route.OperationID != "updateLinkedToken" {
		t.Fatalf("dynamic route = %+v, want updateLinkedToken", route)
	}
	if !routePathMatches("/api/{version}/tokens/{id}", "/api/v1/tokens/123") {
		t.Fatal("route pattern did not match non-empty path parameters")
	}
	for _, path := range []string{"/api/v1/tokens", "/api/v1/tokens/", "/api/v1/tokens/123/extra"} {
		if routePathMatches("/api/{version}/tokens/{id}", path) {
			t.Errorf("route pattern unexpectedly matched %q", path)
		}
	}
}

func TestRouteTextSerializerFlattensStructsAndNestedScalarLists(t *testing.T) {
	type details struct {
		Count int      `json:"count"`
		Tags  []string `json:"tags"`
		Skip  string   `json:"skip,omitempty"`
	}
	type response struct {
		Name    string  `json:"name"`
		Details details `json:"details"`
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/about", nil)
	got := serializeRouteText(request, response{Name: "Git Messages", Details: details{Count: 3, Tags: []string{"public", "curated"}}})
	want := "OK: Retrieved successfully\nname: Git Messages\ndetails.count: 3\ndetails.tags: public, curated"
	if got != want {
		t.Fatalf("serialized struct = %q, want %q", got, want)
	}
}

func TestSwaggerOperationIDIsCamelCaseAndPathSpecific(t *testing.T) {
	tests := map[string]string{
		"getMetrics|/api/v1/server/metrics":      "getMetricsApiV1ServerMetrics",
		"graphqlQuery|/api/graphql":              "graphqlQueryApiGraphql",
		"downloadCliBinary|/cli/binaries/{name}": "downloadCliBinaryCliBinariesName",
	}
	for input, want := range tests {
		parts := strings.SplitN(input, "|", 2)
		if got := swaggerOperationID(parts[0], parts[1]); got != want {
			t.Errorf("swaggerOperationID(%q, %q) = %q, want %q", parts[0], parts[1], got, want)
		}
	}

	original := map[string]any{"post": map[string]any{"summary": "Original"}}
	updated := withOperationID(original, "post", "createMessage")
	if got := updated["post"].(map[string]any)["operationId"]; got != "createMessage" {
		t.Fatalf("operationId = %v, want createMessage", got)
	}
	if _, exists := original["post"].(map[string]any)["operationId"]; exists {
		t.Fatal("withOperationID mutated the original operation")
	}
}
