# API reference

All REST routes use `/api/v1`. JSON responses use an `{ "ok": true, "data": ... }` success envelope and a structured error envelope. No authentication is required; the catalog is public and the builder stores no state.

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/v1/types` | List commit types and emoji mappings |
| GET | `/api/v1/conventions` | List Conventional Commits and Angular conventions |
| GET | `/api/v1/templates` | List message templates |
| GET | `/api/v1/templates/random` | Return a random template |
| GET | `/api/v1/messages/random` | Return a random commit type example |
| POST | `/api/v1/messages` | Validate and build a message from JSON |
| GET | `/api/v1/server/healthz` | Return service health |
| GET | `/api/v1/server/about` | Return service description |
| GET | `/api/v1/server/help` | Return API documentation links |
| GET | `/api/v1/server/swagger` | Return OpenAPI JSON |

Example builder input:

```json
{
  "type": "feat",
  "scope": "api",
  "description": "add message search",
  "body": "",
  "breaking": false,
  "convention": "conventional-commits"
}
```

The result is `feat(api): add message search`. Types must be in the catalog; descriptions are required single lines up to 100 characters; scopes allow letters, numbers, dots, slashes, underscores, and hyphens up to 50 characters; bodies are limited to 5000 characters.

## GraphQL

GraphQL accepts GET query parameters and POST JSON at `/graphql`. The same handler is available at `/api/v1/server/graphql` and `/api/graphql`. `/server/docs/graphql` provides a browser query form.

```graphql
query Catalog {
  types { type description emoji example breaking }
  conventions { name description format example }
  templates { name message category }
}
```

Build a message with a mutation:

```graphql
mutation Build($input: BuildMessageInput!) {
  buildMessage(input: $input) { message }
}
```

Provide `variables.input` with the same fields as the REST builder request.

## OpenAPI

OpenAPI JSON is available at `/api/v1/server/swagger` and its direct alias `/api/swagger`. The browser reference is `/server/docs/swagger`.
