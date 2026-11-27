package swagger

//go:generate go run ../openapi-gen

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"reflect"
	"strings"
)

// SchemaFromModel derives a JSON schema from a Go response or request model.
func SchemaFromModel(model any) map[string]any {
	return schemaForValue(reflect.ValueOf(model))
}

func schemaForValue(value reflect.Value) map[string]any {
	if !value.IsValid() {
		return map[string]any{}
	}
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return schemaForType(value.Type())
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		properties := make(map[string]any)
		required := make([]string, 0)
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if !field.IsExported() {
				continue
			}
			parts := strings.Split(field.Tag.Get("json"), ",")
			name := parts[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			properties[name] = schemaForValue(value.Field(index))
			optional := false
			for _, option := range parts[1:] {
				if option == "omitempty" {
					optional = true
				}
			}
			if !optional {
				required = append(required, name)
			}
		}
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	case reflect.Map:
		properties := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			if iterator.Key().Kind() == reflect.String {
				properties[iterator.Key().String()] = schemaForValue(iterator.Value())
			}
		}
		if value.Len() > 0 {
			return map[string]any{"type": "object", "properties": properties}
		}
		return map[string]any{"type": "object", "additionalProperties": schemaForType(value.Type().Elem())}
	case reflect.Slice, reflect.Array:
		itemSchema := schemaForType(value.Type().Elem())
		if value.Len() > 0 {
			itemSchema = schemaForValue(value.Index(0))
		}
		return map[string]any{"type": "array", "items": itemSchema}
	default:
		return schemaForType(value.Type())
	}
}

func schemaForType(kind reflect.Type) map[string]any {
	if kind == nil {
		return map[string]any{}
	}
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	switch kind.Kind() {
	case reflect.Struct:
		properties := make(map[string]any)
		required := make([]string, 0)
		for index := 0; index < kind.NumField(); index++ {
			field := kind.Field(index)
			if !field.IsExported() {
				continue
			}
			parts := strings.Split(field.Tag.Get("json"), ",")
			name := parts[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			properties[name] = schemaForType(field.Type)
			optional := false
			for _, option := range parts[1:] {
				if option == "omitempty" {
					optional = true
				}
			}
			if !optional {
				required = append(required, name)
			}
		}
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaForType(kind.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaForType(kind.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Interface:
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

//go:embed assets/*
var assets embed.FS

type Document struct {
	Title           string
	Version         string
	Description     string
	ServerURL       string
	Operations      []Operation
	AdditionalPaths map[string]any
}

func Build(document Document) map[string]any {
	paths := make(map[string]any, len(document.Operations)+len(document.AdditionalPaths))
	for _, definition := range document.Operations {
		if definition.Internal {
			continue
		}
		operation := map[string]any{
			"operationId": definition.ID,
			"summary":     definition.Summary,
			"description": definition.Description,
			"responses":   definition.Responses,
		}
		if definition.RequestBody != nil {
			operation["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": definition.RequestBody}}}
		}
		if len(definition.Parameters) > 0 {
			parameters := make([]map[string]any, 0, len(definition.Parameters))
			for _, parameter := range definition.Parameters {
				parameters = append(parameters, map[string]any{"name": parameter.Name, "in": parameter.Location, "required": parameter.Required, "schema": parameter.Schema})
			}
			operation["parameters"] = parameters
		}
		if len(definition.Security) > 0 {
			security := make([]map[string][]string, 0, len(definition.Security))
			for _, scheme := range definition.Security {
				security = append(security, map[string][]string{scheme: {}})
			}
			operation["security"] = security
		}
		mountedWhen := definition.MountedWhen
		if definition.DebugOnly {
			mountedWhen = "debug_enabled"
		}
		if mountedWhen != "" {
			operation["x-mounted-when"] = mountedWhen
		}
		operations, _ := paths[definition.Path].(map[string]any)
		if operations == nil {
			operations = make(map[string]any)
		}
		operations[strings.ToLower(definition.Method)] = operation
		paths[definition.Path] = operations
	}
	for endpoint, operations := range document.AdditionalPaths {
		paths[endpoint] = operations
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]string{
			"title":       document.Title,
			"version":     document.Version,
			"description": document.Description,
		},
		"servers": []map[string]string{{"url": document.ServerURL}},
		"components": map[string]any{"securitySchemes": map[string]any{
			"BearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
		}},
		"paths": paths,
	}
}

// Load returns the OpenAPI superset generated for a supported locale at build time.
func Load(language string) (map[string]any, error) {
	language = strings.ToLower(strings.TrimSpace(language))
	if len(language) != 2 {
		language = "en"
	}
	content, err := assets.ReadFile("assets/openapi-" + language + ".json")
	if err != nil && language != "en" {
		content, err = assets.ReadFile("assets/openapi-en.json")
	}
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, err
	}
	return document, nil
}

// FilterMounted removes operations whose x-mounted-when condition is disabled.
func FilterMounted(document map[string]any, enabled map[string]bool) map[string]any {
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return document
	}
	for path, rawOperations := range paths {
		operations, ok := rawOperations.(map[string]any)
		if !ok {
			continue
		}
		for method, rawOperation := range operations {
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			condition, _ := operation["x-mounted-when"].(string)
			if condition != "" && !enabled[condition] {
				delete(operations, method)
			}
		}
		if len(operations) == 0 {
			delete(paths, path)
		}
	}
	return document
}

func Asset(name string) ([]byte, error) {
	if path.Base(name) != name || name == "." || name == ".." {
		return nil, fs.ErrNotExist
	}
	return assets.ReadFile("assets/" + name)
}
