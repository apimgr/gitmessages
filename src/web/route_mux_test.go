package web

import (
	"net/http"
	"net/http/httptest"
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
