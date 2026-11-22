# API reference

All REST routes use `/api/v1`. JSON responses use an `{ "ok": true, "data": ... }` success envelope and a structured error envelope. No authentication is required; the catalog is public and the builder stores no state.

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/v1` | List API resources and server page endpoints |
| GET | `/api/v1/types` | List commit types and emoji mappings |
| GET | `/api/v1/conventions` | List Conventional Commits and Angular conventions |
| GET | `/api/v1/templates` | List message templates |
| GET | `/api/v1/templates/random` | Return a random template |
| GET | `/api/v1/messages/random` | Return a random commit type example |
| POST | `/api/v1/messages` | Validate and build a message from JSON |
| GET | `/api/v1/server/healthz` | Return service health |
| GET | `/api/v1/server/about` | Return service description |
| GET | `/api/v1/server/help` | Return API documentation links |
| GET | `/api/v1/server/privacy` | Return the localized privacy policy text |
| GET | `/api/v1/server/terms` | Return the localized terms text |
| GET | `/api/v1/server/security` | Return localized security reporting instructions and canonical policy links |
| GET | `/api/v1/server/preferences` | Return the current request language and theme preference |
| GET | `/api/v1/server/help/mcp` | Return localized MCP connection instructions |
| GET | `/api/v1/server/swagger` | Return OpenAPI JSON |
| POST | `/api/v1/server/mcp`, `/api/mcp`, `/mcp` | MCP JSON-RPC tools for the public catalog and stateless message builder |

`/.well-known/security.txt` publishes the security contact, expiration, preferred languages, canonical URL, and reporting policy.

Server-page APIs mirror the public page content. The preferences endpoint returns the current request language and theme. `.txt` variants return plain text when needed by command-line clients. The `lang` query parameter, saved language cookie, or `Accept-Language` header selects the response language.

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

OpenAPI JSON is available at `/api/v1/server/swagger` and its direct alias `/api/swagger`; descriptions follow the request language. The browser reference is `/server/docs/swagger`.

## Model Context Protocol

The Streamable HTTP endpoint accepts JSON-RPC `POST` requests at `/api/v1/server/mcp`. `/api/mcp` and `/mcp` are direct aliases. It exposes each public REST operation and GraphQL queries without authentication; it has no user resources or token-management tools. MCP clients must send the negotiated protocol version in `MCP-Protocol-Version`, the JSON-RPC method in `Mcp-Method`, and (for `tools/call`) the tool name in `Mcp-Name`. The version must also match `params._meta.io.modelcontextprotocol/protocolVersion`. The endpoint validates `Origin` when supplied. See `/server/help/mcp` for the localized tool list and connection details.
