package web

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
)

type serverPageRegistration struct {
	Path        string
	TitleKey    string
	Description string
	Order       int
}

func serverPageRegistry(hub string) []serverPageRegistration {
	var pages []serverPageRegistration
	switch hub {
	case "/server/preferences":
		pages = []serverPageRegistration{
			{Path: "/server/preferences/tokens", TitleKey: "preferences.tokens.title", Description: "preferences.tokens.description", Order: 10},
			{Path: "/server/preferences/sync", TitleKey: "preferences.sync", Description: "preferences.sync.description", Order: 20},
		}
	case "/server/help":
		pages = []serverPageRegistration{{Path: "/server/help/mcp", TitleKey: "mcp.title", Description: "mcp.help.description", Order: 10}}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Order < pages[j].Order })
	return pages
}

func serverHubList(r *http.Request, hub string) string {
	var body strings.Builder
	for _, entry := range serverPageRegistry(hub) {
		fmt.Fprintf(&body, `<li class="hub-item"><a class="hub-link" href="%s"><span class="hub-title">%s</span><span class="hub-desc">%s</span></a></li>`, html.EscapeString(entry.Path), html.EscapeString(tr(r, entry.TitleKey)), html.EscapeString(tr(r, entry.Description)))
	}
	return body.String()
}

func serverHubPages(r *http.Request, hub string) []map[string]string {
	pages := serverPageRegistry(hub)
	result := make([]map[string]string, 0, len(pages))
	for _, entry := range pages {
		result = append(result, map[string]string{"title": tr(r, entry.TitleKey), "path": entry.Path, "description": tr(r, entry.Description)})
	}
	return result
}
