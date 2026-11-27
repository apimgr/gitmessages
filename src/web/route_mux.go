package web

import (
	"net/http"
	"strings"
	"sync"
)

type routeRegistrar interface {
	Handle(string, http.Handler)
	HandleFunc(string, func(http.ResponseWriter, *http.Request))
}

type routeInfo struct {
	Method    string `json:"method"`
	Path      string `json:"route"`
	Internal  bool   `json:"internal,omitempty"`
	DebugOnly bool   `json:"debug_only,omitempty"`
}

type routeMux struct {
	*http.ServeMux
	mu     sync.RWMutex
	routes []routeInfo
}

func newRouteMux() *routeMux {
	return &routeMux{ServeMux: http.NewServeMux()}
}

func (mux *routeMux) Handle(pattern string, handler http.Handler) {
	mux.ServeMux.Handle(pattern, handler)
	mux.record(pattern)
}

func (mux *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.ServeMux.HandleFunc(pattern, handler)
	mux.record(pattern)
}

func (mux *routeMux) HandleDebugOnly(pattern string, handler http.Handler, enabled bool) {
	if !enabled {
		return
	}
	mux.ServeMux.Handle(pattern, handler)
	mux.recordRoute(pattern, true, true)
}

func (mux *routeMux) record(pattern string) {
	mux.recordRoute(pattern, false, false)
}

func (mux *routeMux) recordRoute(pattern string, internal, debugOnly bool) {
	method := "ANY"
	path := pattern
	if separator := strings.IndexByte(pattern, ' '); separator > 0 {
		method = pattern[:separator]
		path = pattern[separator+1:]
	}
	mux.mu.Lock()
	mux.routes = append(mux.routes, routeInfo{Method: method, Path: path, Internal: internal, DebugOnly: debugOnly})
	mux.mu.Unlock()
}

func (mux *routeMux) routeList() []routeInfo {
	mux.mu.RLock()
	defer mux.mu.RUnlock()
	return append([]routeInfo(nil), mux.routes...)
}
