package messages

import (
	"embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"

	"github.com/apimgr/gitmessages/src/common/i18n"
)

//go:embed data/types.json data/conventions.json data/templates.json
var catalogFS embed.FS

// CommitType describes a supported commit type.
type CommitType struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Emoji       string `json:"emoji"`
	Example     string `json:"example"`
	Breaking    bool   `json:"breaking"`
}

// Convention describes a supported commit message convention.
type Convention struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Format      string `json:"format"`
	Example     string `json:"example"`
}

// Template describes a reusable message template.
type Template struct {
	Name     string `json:"name"`
	Message  string `json:"message"`
	Category string `json:"category"`
}

// BuildInput contains the user-selected fields for a commit message.
type BuildInput struct {
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Breaking    bool   `json:"breaking"`
	Convention  string `json:"convention"`
}

var (
	commitTypes  []CommitType
	conventions  []Convention
	msgTemplates []Template
	fieldPattern = regexp.MustCompile(`^[[:alnum:]][[:alnum:]./_-]{0,49}$`)
)

func init() {
	commitTypes = mustLoadCatalog[[]CommitType]("data/types.json")
	conventions = mustLoadCatalog[[]Convention]("data/conventions.json")
	msgTemplates = mustLoadCatalog[[]Template]("data/templates.json")
}

func mustLoadCatalog[T any](path string) T {
	var value T
	data, err := catalogFS.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("read embedded catalog %s: %v", path, err))
	}
	if err := json.Unmarshal(data, &value); err != nil {
		panic(fmt.Sprintf("decode embedded catalog %s: %v", path, err))
	}
	return value
}

// Types returns a copy of the supported commit types.
func Types() []CommitType {
	return append([]CommitType(nil), commitTypes...)
}

// TypesForLocale returns commit types with localized descriptions.
func TypesForLocale(lang string) []CommitType {
	items := Types()
	for index := range items {
		items[index].Description = i18n.T(lang, "catalog.type."+items[index].Type)
	}
	return items
}

// Type returns the named commit type, if it exists.
func Type(name string) (CommitType, bool) {
	for _, item := range commitTypes {
		if item.Type == name {
			return item, true
		}
	}
	return CommitType{}, false
}

// Conventions returns a copy of the supported conventions.
func Conventions() []Convention {
	return append([]Convention(nil), conventions...)
}

// ConventionsForLocale returns conventions with localized descriptions.
func ConventionsForLocale(lang string) []Convention {
	items := Conventions()
	for index := range items {
		items[index].Description = i18n.T(lang, "catalog.convention."+items[index].Name)
	}
	return items
}

// Templates returns a copy of the curated message templates.
func Templates() []Template {
	return append([]Template(nil), msgTemplates...)
}

// TemplatesForLocale returns templates with localized names and categories.
func TemplatesForLocale(lang string) []Template {
	items := Templates()
	for index := range items {
		items[index].Name = i18n.T(lang, "catalog.template."+items[index].Name)
		items[index].Category = i18n.T(lang, "catalog.category."+items[index].Category)
	}
	return items
}

// TemplateByName returns a named template, if it exists.
func TemplateByName(name string) (Template, bool) {
	for _, item := range msgTemplates {
		if item.Name == name {
			return item, true
		}
	}
	return Template{}, false
}

// RandomTemplate returns one curated template.
func RandomTemplate() Template {
	return msgTemplates[rand.IntN(len(msgTemplates))]
}

// Build validates input and formats a commit message using a known convention.
func Build(input BuildInput) (string, error) {
	input.Type = strings.TrimSpace(input.Type)
	input.Scope = strings.TrimSpace(input.Scope)
	input.Description = strings.TrimSpace(input.Description)
	input.Body = strings.TrimSpace(input.Body)
	input.Convention = strings.TrimSpace(input.Convention)

	if _, ok := Type(input.Type); !ok {
		return "", fmt.Errorf("unknown commit type")
	}
	if input.Description == "" || strings.ContainsAny(input.Description, "\r\n") {
		return "", fmt.Errorf("description must be a non-empty single line")
	}
	if len(input.Description) > 100 {
		return "", fmt.Errorf("description must be 100 characters or fewer")
	}
	if input.Scope != "" && !fieldPattern.MatchString(input.Scope) {
		return "", fmt.Errorf("scope must be 1–50 letters, numbers, dots, slashes, underscores, or hyphens")
	}
	if len(input.Body) > 5000 {
		return "", fmt.Errorf("body must be 5000 characters or fewer")
	}
	if input.Convention == "" {
		input.Convention = "conventional-commits"
	}
	if input.Convention != "conventional-commits" && input.Convention != "angular" {
		return "", fmt.Errorf("unknown commit convention")
	}

	kind := input.Type
	if input.Scope != "" {
		kind += "(" + input.Scope + ")"
	}
	if input.Breaking {
		kind += "!"
	}
	message := kind + ": " + input.Description
	if input.Breaking {
		message += "\n\nBREAKING CHANGE: " + input.Description
	}
	if input.Body != "" {
		message += "\n\n" + input.Body
	}
	return message, nil
}
