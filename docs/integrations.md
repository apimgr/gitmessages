# Integrations

Git Messages does not query third-party content providers for catalog or message data. The server downloads operational GeoIP, blocklist, CVE, and optional Trivy security datasets from the configured sources described in [Configuration](configuration.md); these datasets do not alter the curated catalog. It has no user accounts, but supports anonymous owner tokens and the configured contact/security report flows required by AI.md. When available, the server starts its own Tor process for a v3 onion service. I2P is off by default on bare metal and in VMs; containers enable it by default and use the included `i2pd` provider when available. Both expose the same local reference service. Configured hCaptcha or reCAPTCHA performs CAPTCHA verification with that provider, and configured SMTP relays send contact and security-report email; neither integration is required for the public catalog.

## HTTP clients

Any HTTP client can use the public catalog REST API. The companion CLI uses the configured server and sends a project-specific User-Agent. OpenAPI JSON is available at `/api/{api_version}/server/swagger`; GraphQL accepts POST requests at `/api/{api_version}/server/graphql` and `/api/graphql`; MCP Streamable HTTP is available at `/api/{api_version}/server/mcp` and the `/api/mcp` and `/mcp` aliases and requires an owner or operator Bearer token. Health is available at `/api/{api_version}/server/healthz` and its direct `/api/healthz` alias. The optional `/healthz` alias is disabled by default and enabled with `server.healthz.root.enabled`.

`GET /api/autodiscover` returns the active API version, public origins, exposed overlay addresses, CLI versions with verified SHA-256 checksums, and the minimum accepted CLI version. The CLI binaries are provisioned by the operator in `{data_dir}/cli/` and downloaded from `/cli/binaries/{project_name}-cli-{os}-{arch}[.exe]`; downloads are public unless `cli.binary_download.require_auth` is enabled. The CLI checks for updates on each invocation and installs only over HTTPS after checksum verification. See [API autodiscovery](api.md#autodiscovery) and the [CLI reference](cli.md#client-binary).

## Discovery and security reporting

The server always publishes `/.well-known/security.txt` for vulnerability reporting and `/.well-known/llms.txt` for agent discovery; the latter is also available at `/llms.txt`. A configured public PGP key is available at `/.well-known/pgp-key.asc`. Unsupported `/.well-known/*` paths return `404`; this project does not enable WebFinger, OAuth/OIDC metadata, Android or Apple app association, or MTA-STS endpoints. See [Security](security.md) for the disclosure workflow and [API](api.md) for the API and interactive documentation routes.

The server also publishes `/robots.txt` and `/sitemap.xml` for crawler discovery. The sitemap contains only public HTML pages permitted by the configured robots rules; it excludes API routes and the contact form. `/feed.xml` (Atom) and `/rss.xml` (RSS) are available for feed readers and currently contain no entries because Git Messages has no public per-item resource. These endpoints use request-specific absolute URLs. See [Browser application](browser.md) for response and cache behavior.

MCP exposes tools for the REST operations and a GraphQL POST query. The tools include `get_api_index`, `list_commit_types`, `list_conventions`, `list_templates`, `get_random_template`, `get_random_example`, `build_commit_message`, `get_health`, `get_about`, `get_help`, `get_mcp_help`, `get_privacy`, `get_terms`, `get_security`, `get_security_policy`, `get_security_thanks`, `get_data_protection_contact`, `get_contact_form`, `submit_contact_report`, `get_preferences`, `save_preferences`, `get_preferences_sync`, `import_preferences_sync`, `get_openapi`, and `graphql_query`. Token management and metrics are not exposed as tools. The localized `/server/help/mcp` page and the API tool list use the same operation definitions.

## Git command pipelines

The CLI prints a single commit example from `random`, suitable for shell pipelines:

```sh
GITMESSAGES_SERVER=http://127.0.0.1:8080
git commit -m "$(gitmessages-cli --server "$GITMESSAGES_SERVER" random)"
```
