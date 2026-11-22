# API reference

REST resources use `/api/v1`. JSON responses use an `{ "ok": true, "data": ... }` success envelope and a structured error envelope. The catalog, ordinary server pages, health, OpenAPI, and GraphQL are public. MCP, metrics, owner-token management, and operator-only routes require the Bearer credentials defined by the server specification. The message builder stores no state.

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
| POST | `/api/v1/server/preferences` | Save supported language and theme preferences |
| GET | `/api/v1/server/preferences/sync` | Export portable preferences as JSON with an import URL and code |
| GET | `/api/v1/server/preferences/sync/import` | Apply preferences from the supplied import code |
| GET | `/api/v1/server/help/mcp` | Return localized MCP connection instructions |
| GET | `/api/v1/server/preferences/tokens` | List masked owner tokens; requires the owner's primary token |
| POST | `/api/v1/server/preferences/tokens/primary` | Issue a primary owner token for a new owner; no token required |
| POST | `/api/v1/server/preferences/tokens/primary/reveal` | Reveal the primary owner token; requires the primary token |
| POST | `/api/v1/server/preferences/tokens/primary/rotate` | Rotate the primary owner token; requires the primary token |
| POST | `/api/v1/server/preferences/tokens` | Create a linked token; requires the primary token |
| POST | `/api/v1/server/preferences/tokens/{id}/reveal` | Reveal a linked token; requires the primary token |
| POST | `/api/v1/server/preferences/tokens/{id}/rotate` | Rotate a linked token; requires the primary token |
| PATCH | `/api/v1/server/preferences/tokens/{id}` | Rename or change linked token expiry; requires the primary token |
| DELETE | `/api/v1/server/preferences/tokens/{id}` | Revoke a linked token; requires the primary token |
| GET | `/api/v1/server/metrics[/{service}]` | Internal service metrics; requires its per-service Bearer token |
| GET | `/api/v1/server/contact` | Return contact form descriptors and reporting information |
| POST | `/api/v1/server/contact` | Submit the selected contact or security report; form and security anti-spam requirements apply |
| GET | `/api/v1/server/dpo` | Return the configured privacy contact |
| GET | `/api/v1/server/security/policy` | Return the disclosure policy |
| GET | `/api/v1/server/security/thanks` | Return public researcher acknowledgments |
| GET | `/api/v1/server/swagger` | Return OpenAPI JSON |
| POST | `/api/v1/server/mcp`, `/api/mcp`, `/mcp` | MCP JSON-RPC tools for the public catalog and stateless message builder |

`/.well-known/security.txt` publishes the security contact, expiration, preferred languages, canonical URL, and reporting policy.

Server-page APIs mirror public page content. The preferences endpoint returns the current request language and theme. `.txt` variants return plain text when available. A `?lang=` link pins the language in a cookie; otherwise a saved language cookie or `Accept-Language` selects it.

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

The Streamable HTTP endpoint accepts JSON-RPC `POST` requests at `/api/v1/server/mcp`; `/api/mcp` and `/mcp` are direct aliases. Every request requires `Authorization: Bearer …` using an operator or owner token. It exposes the public catalog and stateless message builder, never token-management tools. MCP clients send `MCP-Protocol-Version`, `Mcp-Method`, and for named operations `Mcp-Name`; values must match the JSON-RPC body. Origin, authentication, protocol version, and headers are checked in the order specified by AI.md. See `/server/help/mcp` for connection details.
