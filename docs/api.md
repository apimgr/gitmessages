# API reference

The canonical API prefix is `/api/{api_version}`; its default is `/api/v1`, and the server advertises the active version through the unversioned `GET /api/autodiscover` endpoint. Clients should discover the version before constructing canonical API URLs. JSON responses use an `{ "ok": true, "data": ... }` success envelope and a structured error envelope. The catalog, ordinary server pages, health, OpenAPI, and GraphQL are public. MCP, metrics, owner-token management, and operator-only routes require the Bearer credentials defined by the server specification. The message builder stores no state.

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/autodiscover` | Discover public server origins, API version, exposed overlay addresses, available CLI updates, and minimum CLI version |
| GET | `/cli/binaries/{project_name}-cli-{os}-{arch}[.exe]` | Download an operator-provisioned CLI release binary; public by default, or Bearer-authenticated when configured |
| GET | `/api/{api_version}` | List API resources and server page endpoints |
| GET | `/api/{api_version}/types` | List commit types and emoji mappings |
| GET | `/api/{api_version}/conventions` | List Conventional Commits and Angular conventions |
| GET | `/api/{api_version}/templates` | List message templates |
| GET | `/api/{api_version}/templates/random` | Return a random template |
| GET | `/api/{api_version}/messages/random` | Return a random commit type example |
| POST | `/api/{api_version}/messages` | Validate and build a message from JSON |
| GET | `/api/{api_version}/server/healthz` | Return service health |
| GET | `/api/{api_version}/server/about` | Return service description |
| GET | `/api/{api_version}/server/help` | Return API documentation links |
| GET | `/api/{api_version}/server/privacy` | Return the localized privacy policy text |
| GET | `/api/{api_version}/server/terms` | Return the localized terms text |
| GET | `/api/{api_version}/server/security` | Return localized security reporting instructions and canonical policy links |
| GET | `/api/{api_version}/server/preferences` | Return the current request language and theme preference |
| POST | `/api/{api_version}/server/preferences` | Save supported language and theme preferences |
| GET | `/api/{api_version}/server/preferences/sync` | Export portable preferences as JSON with an import URL and code |
| GET | `/api/{api_version}/server/preferences/sync/import` | Apply preferences from the supplied import code |
| GET | `/api/{api_version}/server/help/mcp` | Return localized MCP connection instructions |
| GET | `/api/{api_version}/server/preferences/tokens` | List masked owner tokens; requires the owner's primary token |
| POST | `/api/{api_version}/server/preferences/tokens/primary` | Issue a primary owner token for a new owner; no token required |
| POST | `/api/{api_version}/server/preferences/tokens/primary/reveal` | Reveal the primary owner token; requires the primary token |
| POST | `/api/{api_version}/server/preferences/tokens/primary/rotate` | Rotate the primary owner token; requires the primary token |
| DELETE | `/api/{api_version}/server/preferences/tokens/primary` | Delete the owner and all its tokens; requires the primary token and a JSON body containing the last four token characters in `confirm` |
| POST | `/api/{api_version}/server/preferences/tokens` | Create a linked token; requires the primary token |
| POST | `/api/{api_version}/server/preferences/tokens/{id}/reveal` | Reveal a linked token; requires the primary token |
| POST | `/api/{api_version}/server/preferences/tokens/{id}/rotate` | Rotate a linked token; requires the primary token |
| PATCH | `/api/{api_version}/server/preferences/tokens/{id}` | Rename or change linked token expiry; requires the primary token |
| DELETE | `/api/{api_version}/server/preferences/tokens/{id}` | Revoke a linked token; requires the primary token |
| GET | `/api/{api_version}/server/metrics[/{service}]` | Internal service metrics; requires its per-service Bearer token |
| GET | `/api/{api_version}/server/contact` | Return contact form descriptors and reporting information |
| POST | `/api/{api_version}/server/contact` | Submit the selected contact or security report; form and security anti-spam requirements apply |
| GET | `/api/{api_version}/server/dpo` | Return the configured privacy contact |
| GET | `/api/{api_version}/server/security/policy` | Return the disclosure policy |
| GET | `/api/{api_version}/server/security/thanks` | Return public researcher acknowledgments |
| GET | `/api/{api_version}/server/swagger` | Return OpenAPI JSON |
| POST | `/api/{api_version}/server/mcp`, `/api/mcp`, `/mcp` | MCP JSON-RPC tools for the public catalog and stateless message builder |

## Autodiscovery

`GET /api/autodiscover` is intentionally unversioned. Its success envelope contains `name`, `version`, `api.current_version`, public `urls`, `cli_versions`, and `cli_min_version`. The `onion` and `i2p` fields appear only when the operator enables their respective exposure settings. The response contains no configuration schema, option list, or credentials.

CLI release files are provisioned by the operator under `{data_dir}/cli/` (or `cli.binary_download.dir` in `yaml.d/maintenance.yml`). The directory contains `{project_name}-cli-{os}-{arch}[.exe]`, `version.txt`, and `sha256.txt`. The server computes each binary's SHA-256 and advertises only entries whose checksum matches. An empty or missing directory advertises no updates, and an unavailable binary returns `404`. Set `cli.binary_download.require_auth` to require a valid Bearer token for downloads; the default is public access.

The server defaults to API version `v1`. Operators may change `server.api_version` to another `v`-prefixed numeric version; changing it requires a restart. Clients should use the discovered `server.api_base` for canonical routes. `/api/swagger`, `/api/graphql`, `/api/healthz`, and `/api/mcp` remain direct aliases for the active version's handlers.

`/.well-known/security.txt` publishes the security contact, expiration, preferred languages, canonical URL, and reporting policy.

## Direct aliases and browser documentation

| Route | Behavior |
|---|---|
| `/api/swagger` | Direct alias for the OpenAPI JSON document. |
| `/api/graphql` | Direct alias for the GraphQL handler. |
| `/api/healthz` | Unversioned direct health endpoint. |
| `/api/metrics[/{service}]` | Direct alias for the versioned metrics handlers; each service requires its Bearer token. |
| `/metrics[/{service}]` | Root metrics aliases; enabled by default when metrics are enabled, and each service requires its Bearer token. |
| `/healthz` | Optional direct alias for `/server/healthz`; disabled by default and controlled by `server.healthz.root.enabled`. |
| `/server/docs/swagger` | Interactive REST explorer. JavaScript loads the versioned OpenAPI document; without JavaScript, follow the link to the JSON specification. |
| `/server/docs/graphql` | GraphiQL query explorer with a no-JavaScript POST form. |
| `/server/help/mcp` | Browser MCP connection guide. |

Metrics can also be reached through `/server/metrics[/{service}]` and the versioned `/api/{api_version}/server/metrics[/{service}]` routes. See [Metrics](metrics.md) for service names and token configuration.

Server-page APIs mirror public page content. The preferences endpoint returns the current request language and theme. `.txt` variants return plain text when available. A `?lang=` link selects the language for that request. It writes a preference cookie only after that browser accepts the Preferences cookie category; otherwise the server uses `Accept-Language` for subsequent requests. Preference writes/imports without consent return an empty `applied` list, list valid values under `ignored`, and include `data.consent_required: true`; web forms redirect with `consent_required=1`.

API routes support the `.txt` suffix and `Accept: text/plain`; non-interactive API clients also receive text by default. Text output follows the route's response model: primary values are bare, objects use an `OK:` line followed by fields in model order, and lists include an item count. Nested fields use dot notation. Health uses its documented flat layout without an `OK:` line; binary downloads retain their binary content type. Errors keep their HTTP status and use `ERROR: {code}: {message}`.

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

GraphQL accepts POST requests at `/api/{api_version}/server/graphql` and its direct alias `/api/graphql`. The JSON API accepts a `query` and optional `variables`; the browser provides GraphiQL and a no-JavaScript URL-encoded POST form. The removed root `/graphql` route is not served.

```graphql
query Catalog {
  types { type description emoji example breaking }
  conventions { name description format example }
  templates { name message category }
}
```

Build a message with a read-only query:

```graphql
query Build($input: BuildMessageInput!) {
  buildMessage(input: $input) { message }
}
```

Provide `variables.input` with the same fields as the REST builder request.

## OpenAPI

OpenAPI JSON is available at `/api/{api_version}/server/swagger` and its direct alias `/api/swagger`; descriptions follow the request language. The browser reference at `/server/docs/swagger` loads the spec from the same origin and provides a Try it control for each operation. It supports Bearer-token entry for protected operations, never saves the token, and runs requests against the server URL in the spec. Without JavaScript, the page links directly to the JSON document.

The GraphiQL explorer at `/server/docs/graphql` submits queries to `/api/{api_version}/server/graphql`. A browser form submission with JavaScript disabled returns an HTML page containing the escaped JSON result; API clients continue to receive JSON. The page also links to the plain GraphQL endpoint. The direct `/api/graphql` alias remains available to clients.

## Model Context Protocol

The Streamable HTTP endpoint accepts JSON-RPC `POST` requests at `/api/{api_version}/server/mcp`; `/api/mcp` and `/mcp` are direct aliases. Every request requires `Authorization: Bearer …` using an operator or owner token. It exposes the public API operations the caller may use, including catalog and server-information reads, the stateless message builder, and the configured contact-report flow. Token-management and issuance, browser preference, and metrics operations are not tools. MCP clients send `MCP-Protocol-Version`, `Mcp-Method`, and for named operations `Mcp-Name`; values must match the JSON-RPC body. Origin, authentication, protocol version, and headers are checked in the order specified by AI.md. See `/server/help/mcp` for connection details.
