package graphqlapi

import (
	"context"
	"errors"
	"math/rand/v2"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/messages"
	gql "github.com/graphql-go/graphql"
)

func buildSchema(language string) gql.Schema {
	typeObject := gql.NewObject(gql.ObjectConfig{Name: "CommitType", Description: i18n.T(language, "graphql.schema.type.commit_type"), Fields: gql.Fields{
		"type":        &gql.Field{Type: gql.NewNonNull(gql.String), Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.CommitType).Type, nil }},
		"description": &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.CommitType).Description, nil }},
		"emoji":       &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.CommitType).Emoji, nil }},
		"example":     &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.CommitType).Example, nil }},
		"breaking":    &gql.Field{Type: gql.Boolean, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.CommitType).Breaking, nil }},
	}})

	conventionObject := gql.NewObject(gql.ObjectConfig{Name: "Convention", Description: i18n.T(language, "graphql.schema.type.convention"), Fields: gql.Fields{
		"name":        &gql.Field{Type: gql.NewNonNull(gql.String), Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Convention).Name, nil }},
		"description": &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Convention).Description, nil }},
		"format":      &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Convention).Format, nil }},
		"example":     &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Convention).Example, nil }},
	}})

	templateObject := gql.NewObject(gql.ObjectConfig{Name: "MessageTemplate", Description: i18n.T(language, "graphql.schema.type.message_template"), Fields: gql.Fields{
		"name":     &gql.Field{Type: gql.NewNonNull(gql.String), Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Template).Name, nil }},
		"message":  &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Template).Message, nil }},
		"category": &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(messages.Template).Category, nil }},
	}})

	builtObject := gql.NewObject(gql.ObjectConfig{Name: "BuiltMessage", Description: i18n.T(language, "graphql.schema.type.built_message"), Fields: gql.Fields{
		"message": &gql.Field{Type: gql.String, Resolve: func(p gql.ResolveParams) (any, error) { return p.Source.(map[string]string)["message"], nil }},
	}})

	inputObject := gql.NewInputObject(gql.InputObjectConfig{Name: "BuildMessageInput", Description: i18n.T(language, "graphql.schema.type.build_input"), Fields: gql.InputObjectConfigFieldMap{
		"type":        &gql.InputObjectFieldConfig{Type: gql.NewNonNull(gql.String)},
		"scope":       &gql.InputObjectFieldConfig{Type: gql.String},
		"description": &gql.InputObjectFieldConfig{Type: gql.NewNonNull(gql.String)},
		"body":        &gql.InputObjectFieldConfig{Type: gql.String},
		"breaking":    &gql.InputObjectFieldConfig{Type: gql.Boolean},
		"convention":  &gql.InputObjectFieldConfig{Type: gql.String},
	}})

	query := gql.NewObject(gql.ObjectConfig{Name: "Query", Description: i18n.T(language, "graphql.schema.type.query"), Fields: gql.Fields{
		"types": &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(typeObject))), Description: i18n.T(language, "graphql.schema.field.types"), Resolve: func(p gql.ResolveParams) (any, error) {
			return messages.TypesForLocale(localeFromContext(p.Context)), nil
		}},
		"conventions": &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(conventionObject))), Description: i18n.T(language, "graphql.schema.field.conventions"), Resolve: func(p gql.ResolveParams) (any, error) {
			return messages.ConventionsForLocale(localeFromContext(p.Context)), nil
		}},
		"templates": &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(templateObject))), Description: i18n.T(language, "graphql.schema.field.templates"), Resolve: func(p gql.ResolveParams) (any, error) {
			return messages.TemplatesForLocale(localeFromContext(p.Context)), nil
		}},
		"random": &gql.Field{Type: gql.NewNonNull(typeObject), Description: i18n.T(language, "graphql.schema.field.random"), Resolve: func(p gql.ResolveParams) (any, error) {
			all := messages.TypesForLocale(localeFromContext(p.Context))
			return all[rand.IntN(len(all))], nil
		}},
		"buildMessage": &gql.Field{Type: builtObject, Description: i18n.T(language, "graphql.schema.field.build_message"), Args: gql.FieldConfigArgument{"input": &gql.ArgumentConfig{Type: gql.NewNonNull(inputObject)}}, Resolve: func(p gql.ResolveParams) (any, error) {
			inputMap, ok := p.Args["input"].(map[string]any)
			if !ok {
				return nil, nil
			}
			input := messages.BuildInput{Type: stringValue(inputMap["type"]), Scope: stringValue(inputMap["scope"]), Description: stringValue(inputMap["description"]), Body: stringValue(inputMap["body"]), Convention: stringValue(inputMap["convention"])}
			input.Breaking, _ = inputMap["breaking"].(bool)
			message, err := messages.Build(input)
			if err != nil {
				return nil, errors.New(i18n.T(localeFromContext(p.Context), "api.invalid_message"))
			}
			return map[string]string{"message": message}, nil
		}},
	}})

	schema, err := gql.NewSchema(gql.SchemaConfig{Query: query})
	if err != nil {
		panic(err)
	}
	return schema
}

type languageContextKey struct{}

func localeFromContext(ctx context.Context) string {
	lang, _ := ctx.Value(languageContextKey{}).(string)
	return lang
}

// WithLanguage adds the request locale for localized GraphQL field results.
func WithLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, languageContextKey{}, lang)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
