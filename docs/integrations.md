# Integrations

Git Messages does not query third-party content or data providers and has no webhooks, federation, authentication provider, or user-submitted data flow. When available, the server starts its own Tor process for a v3 onion service. I2P is opt-in on bare metal and in VMs; containers enable it by default and use the included `i2pd` provider when available. Both expose the same local public reference service.

## HTTP clients

Any HTTP client can use the public REST API. The companion CLI uses the configured server and sends a project-specific User-Agent. OpenAPI JSON is available at `/api/v1/server/swagger`; GraphQL is available at `/graphql`; MCP Streamable HTTP is available at `/api/v1/server/mcp` and the `/api/mcp` and `/mcp` aliases.

MCP offers the same public catalog and stateless message builder without authentication. It supports `list_commit_types`, `list_conventions`, `list_templates`, `get_random_template`, `get_random_example`, and `build_commit_message`. See `/server/help/mcp` for connection details.

## Git command pipelines

The CLI prints a single commit example from `random`, suitable for shell pipelines:

```sh
git commit -m "$(gitmessages-cli random)"
```
