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
	Method string `json:"method"`
	Path   string `json:"route"`
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

func (mux *routeMux) record(pattern string) {
	method := "ANY"
	path := pattern
	if separator := strings.IndexByte(pattern, ' '); separator > 0 {
		method = pattern[:separator]
		path = pattern[separator+1:]
	}
	mux.mu.Lock()
	mux.routes = append(mux.routes, routeInfo{Method: method, Path: path})
	mux.mu.Unlock()
}

func (mux *routeMux) routeList() []routeInfo {
	mux.mu.RLock()
	defer mux.mu.RUnlock()
	return append([]routeInfo(nil), mux.routes...)
}
