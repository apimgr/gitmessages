package graphqlapi

import (
	"sync"

	gql "github.com/graphql-go/graphql"
)

var schemas sync.Map

func Schema(language string) gql.Schema {
	if cached, ok := schemas.Load(language); ok {
		return cached.(gql.Schema)
	}
	schema := buildSchema(language)
	actual, _ := schemas.LoadOrStore(language, schema)
	return actual.(gql.Schema)
}
