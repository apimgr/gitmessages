package swagger

import "testing"

func TestBuildIncludesConditionalSupersetAndFiltersMountedRoutes(t *testing.T) {
	document := Document{
		Operations: []Operation{
			{Path: "/metrics", Method: "GET", ID: "root_metrics", MountedWhen: "server.metrics.root.enabled"},
			{Path: "/api/metrics", Method: "GET", ID: "api_metrics", MountedWhen: "server.metrics.enabled"},
			{Path: "/api/types", Method: "GET", ID: "types"},
		},
	}
	superset := Build(document)
	paths := superset["paths"].(map[string]any)
	if len(paths) != 3 {
		t.Fatalf("Build returned %d paths; want the full route superset", len(paths))
	}
	rootOperation := paths["/metrics"].(map[string]any)["get"].(map[string]any)
	if rootOperation["x-mounted-when"] != "server.metrics.root.enabled" {
		t.Fatalf("root metrics mount condition = %v", rootOperation["x-mounted-when"])
	}
	served := FilterMounted(superset, map[string]bool{"server.metrics.enabled": true})
	servedPaths := served["paths"].(map[string]any)
	if _, exists := servedPaths["/metrics"]; exists {
		t.Fatal("disabled root metrics route remained in the served document")
	}
	if _, exists := servedPaths["/api/metrics"]; !exists {
		t.Fatal("enabled metrics route was removed from the served document")
	}
	if _, exists := servedPaths["/api/types"]; !exists {
		t.Fatal("unconditional API route was removed from the served document")
	}
}
