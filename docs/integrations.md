# Integrations

Git Messages does not query third-party content or data providers and has no webhooks, federation, authentication provider, or user-submitted data flow. When available, the server starts its own Tor process for a v3 onion service. I2P is opt-in on bare metal and in VMs; containers enable it by default and use the included `i2pd` provider when available. Both expose the same local public reference service.

## HTTP clients

Any HTTP client can use the public REST API. The companion CLI uses the configured server and sends a project-specific User-Agent. OpenAPI JSON is available at `/api/v1/server/swagger`; GraphQL is available at `/graphql`; MCP Streamable HTTP is available at `/api/v1/server/mcp` and the `/api/mcp` and `/mcp` aliases.

MCP exposes one tool for each public REST operation, plus GET and POST GraphQL query tools: `get_api_index`, `list_commit_types`, `list_conventions`, `list_templates`, `get_random_template`, `get_random_example`, `build_commit_message`, `get_health`, `get_about`, `get_help`, `get_mcp_help`, `get_privacy`, `get_terms`, `get_security`, `get_preferences`, `get_openapi`, `graphql_query_get`, and `graphql_query`. The localized `/server/help/mcp` page and the API tool list are generated from the same operation definitions.

## Git command pipelines

The CLI prints a single commit example from `random`, suitable for shell pipelines:

```sh
git commit -m "$(gitmessages-cli random)"
```
