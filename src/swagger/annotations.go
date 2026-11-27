package swagger

type Parameter struct {
	Name     string
	Location string
	Required bool
	Schema   map[string]any
}

type Operation struct {
	Path        string
	Method      string
	ID          string
	Summary     string
	Description string
	RequestBody map[string]any
	Responses   map[string]any
	Parameters  []Parameter
	Security    []string
	MountedWhen string
	Internal    bool
	DebugOnly   bool
}
