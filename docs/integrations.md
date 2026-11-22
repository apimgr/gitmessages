# Integrations

Git Messages does not query third-party content or data providers. It has no user accounts, but supports anonymous owner tokens and the configured contact/security report flows required by AI.md. When available, the server starts its own Tor process for a v3 onion service. I2P is off by default on bare metal and in VMs; containers enable it by default and use the included `i2pd` provider when available. Both expose the same local reference service.

## HTTP clients

Any HTTP client can use the public catalog REST API. The companion CLI uses the configured server and sends a project-specific User-Agent. OpenAPI JSON is available at `/api/v1/server/swagger`; GraphQL is available at `/graphql`; MCP Streamable HTTP is available at `/api/v1/server/mcp` and the `/api/mcp` and `/mcp` aliases and requires an owner or operator Bearer token.

MCP exposes tools for the REST operations and GET/POST GraphQL. The tools include `get_api_index`, `list_commit_types`, `list_conventions`, `list_templates`, `get_random_template`, `get_random_example`, `build_commit_message`, `get_health`, `get_about`, `get_help`, `get_mcp_help`, `get_privacy`, `get_terms`, `get_security`, `get_security_policy`, `get_security_thanks`, `get_data_protection_contact`, `get_contact_form`, `submit_contact_report`, `get_preferences`, `save_preferences`, `get_preferences_sync`, `import_preferences_sync`, `get_openapi`, `graphql_query_get`, and `graphql_query`. Token management and metrics are not exposed as tools. The localized `/server/help/mcp` page and the API tool list use the same operation definitions.

## Git command pipelines

The CLI prints a single commit example from `random`, suitable for shell pipelines:

```sh
git commit -m "$(gitmessages-cli random)"
```
