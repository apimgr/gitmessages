package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDebugOnlyRouteRegistrationIsIndependentFromInternalVisibility(t *testing.T) {
	ordinary := apiOperation{Listed: true}
	if !apiOperationMounted(ordinary, false) || !apiOperationVisible(ordinary, false) {
		t.Fatal("ordinary route should always mount and remain visible")
	}

	internal := apiOperation{Listed: true, Internal: true}
	if !apiOperationMounted(internal, false) || apiOperationVisible(internal, false) {
		t.Fatal("internal visibility must not control route mounting")
	}

	debug := apiOperation{Listed: true, DebugOnly: true}
	if apiOperationMounted(debug, false) || apiOperationVisible(debug, false) {
		t.Fatal("debug-only route should not mount outside debug mode")
	}
	if !apiOperationMounted(debug, true) || !apiOperationVisible(debug, true) {
		t.Fatal("debug-only route should mount in debug mode independently of Internal")
	}
}

func TestRouteMuxOmitsDisabledDebugOnlyRoutes(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	productionMux := newRouteMux()
	productionMux.HandleDebugOnly("/debug/", handler, false)
	if routes := productionMux.routeList(); len(routes) != 0 {
		t.Fatalf("production route registry = %+v, want no debug route", routes)
	}
	productionResponse := httptest.NewRecorder()
	productionMux.ServeHTTP(productionResponse, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))
	if productionResponse.Code != http.StatusNotFound {
		t.Fatalf("production debug response = %d, want 404", productionResponse.Code)
	}

	debugMux := newRouteMux()
	debugMux.HandleDebugOnly("/debug/", handler, true)
	routes := debugMux.routeList()
	if len(routes) != 1 || routes[0].Path != "/debug/" || !routes[0].Internal || !routes[0].DebugOnly {
		t.Fatalf("debug route registry = %+v, want mounted Internal DebugOnly route", routes)
	}
	debugResponse := httptest.NewRecorder()
	debugMux.ServeHTTP(debugResponse, httptest.NewRequest(http.MethodGet, "/debug/vars", nil))
	if debugResponse.Code != http.StatusNoContent {
		t.Fatalf("debug response = %d, want 204", debugResponse.Code)
	}
}

func TestRouteRegistryMountsEveryAPIContractOnce(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	definitions := apiOperationDefinitions(request)
	mux := newRouteMux()
	registerAPIRoutes(mux)
	routes := mux.routeList()
	if len(routes) != len(definitions) {
		t.Fatalf("mounted API routes = %d, route definitions = %d", len(routes), len(definitions))
	}
	for index, definition := range definitions {
		if got := routes[index]; got.Method != definition.Method || got.Path != definition.Path {
			t.Fatalf("mounted route %d = %s %s, want %s %s", index, got.Method, got.Path, definition.Method, definition.Path)
		}
	}
	operationIDs := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if definition.OperationID == "" || strings.Contains(definition.OperationID, "_") || definition.OperationID[0] < 'a' || definition.OperationID[0] > 'z' {
			t.Errorf("route %s %s has invalid camelCase operation ID %q", definition.Method, definition.Path, definition.OperationID)
		}
		if _, exists := operationIDs[definition.OperationID]; exists {
			t.Errorf("route registry repeats operation ID %q", definition.OperationID)
		}
		operationIDs[definition.OperationID] = struct{}{}
	}
	for _, expected := range []string{"/api/autodiscover", "/api/graphql", "/api/mcp", "/mcp", APIBasePath() + "/server/preferences/tokens/{id}"} {
		found := false
		for _, route := range routes {
			if route.Path == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("route registry omitted API endpoint %s", expected)
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, APIBasePath()+"/types", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported API method status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}
