package swagger

import (
	"reflect"
	"testing"
)

func TestSchemaFromModelBuildsNestedJSONSchema(t *testing.T) {
	type nested struct {
		Name string `json:"name"`
	}
	type model struct {
		Title    string         `json:"title"`
		Optional *bool          `json:"optional,omitempty"`
		Items    []nested       `json:"items"`
		Labels   map[string]int `json:"labels"`
		Ignored  string         `json:"-"`
	}
	schema := SchemaFromModel(model{})
	properties := schema["properties"].(map[string]any)
	if schema["type"] != "object" || properties["title"].(map[string]any)["type"] != "string" {
		t.Fatalf("unexpected object schema: %#v", schema)
	}
	if _, ok := properties["optional"]; !ok {
		t.Fatal("pointer field missing")
	}
	if _, ok := properties["Ignored"]; ok {
		t.Fatal("ignored field was included")
	}
	items := properties["items"].(map[string]any)
	if items["type"] != "array" || items["items"].(map[string]any)["type"] != "object" {
		t.Fatalf("unexpected array schema: %#v", items)
	}
	labels := properties["labels"].(map[string]any)
	if labels["additionalProperties"].(map[string]any)["type"] != "integer" {
		t.Fatalf("unexpected map schema: %#v", labels)
	}
	if !reflect.DeepEqual(schema["required"], []string{"title", "items", "labels"}) {
		t.Fatalf("required properties = %#v", schema["required"])
	}
	for _, item := range []struct {
		value any
		want  string
	}{{true, "boolean"}, {int64(1), "integer"}, {float64(1), "number"}, {"x", "string"}, {[]string{}, "array"}, {map[string]string{}, "object"}} {
		actual := SchemaFromModel(item.value)["type"]
		if actual != item.want {
			t.Errorf("SchemaFromModel(%T) type = %v, want %s", item.value, actual, item.want)
		}
	}
	if got := SchemaFromModel(nil); len(got) != 0 {
		t.Fatalf("nil model schema = %#v", got)
	}
}

func TestSchemaFromModelInfersNestedInterfaceMapValues(t *testing.T) {
	schema := SchemaFromModel(map[string]any{
		"data": map[string]any{
			"token": map[string]any{"id": "019535d4-9df7-7b2e-8c3a-c792722a4f12"},
		},
	})
	data := schema["properties"].(map[string]any)["data"].(map[string]any)
	token := data["properties"].(map[string]any)["token"].(map[string]any)
	id := token["properties"].(map[string]any)["id"].(map[string]any)
	if id["type"] != "string" {
		t.Fatalf("nested interface map ID schema = %#v, want string", id)
	}
}

func TestBuildIncludesConditionalSupersetAndFiltersMountedRoutes(t *testing.T) {
	document := Document{
		Operations: []Operation{
			{Path: "/debug/pprof", Method: "GET", ID: "internal_debug", Internal: true},
			{Path: "/debug/diagnostics", Method: "GET", ID: "debug_diagnostics", DebugOnly: true},
			{Path: "/metrics", Method: "GET", ID: "root_metrics", MountedWhen: "server.metrics.root.enabled"},
			{Path: "/api/metrics", Method: "GET", ID: "api_metrics", MountedWhen: "server.metrics.enabled"},
			{Path: "/api/types", Method: "GET", ID: "types"},
		},
	}
	superset := Build(document)
	paths := superset["paths"].(map[string]any)
	if len(paths) != 4 {
		t.Fatalf("Build returned %d paths; want the public route superset without internal routes", len(paths))
	}
	if _, exists := paths["/debug/pprof"]; exists {
		t.Fatal("internal debug route appeared in OpenAPI")
	}
	rootOperation := paths["/metrics"].(map[string]any)["get"].(map[string]any)
	if rootOperation["x-mounted-when"] != "server.metrics.root.enabled" {
		t.Fatalf("root metrics mount condition = %v", rootOperation["x-mounted-when"])
	}
	debugOperation := paths["/debug/diagnostics"].(map[string]any)["get"].(map[string]any)
	if debugOperation["x-mounted-when"] != "debug_enabled" {
		t.Fatalf("debug route mount condition = %v", debugOperation["x-mounted-when"])
	}
	served := FilterMounted(superset, map[string]bool{"server.metrics.enabled": true, "debug_enabled": false})
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
	if _, exists := servedPaths["/debug/diagnostics"]; exists {
		t.Fatal("debug-only route remained mounted in the production document")
	}
}
