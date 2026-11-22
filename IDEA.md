## Project description

Gitmessages is a full-stack Go web application serving a curated reference for git commit message conventions. It provides commit type definitions (feat, fix, docs, style, refactor, perf, test, chore, ci, build, revert), emoji mappings, usage guidelines, and example commit messages through a versioned REST API, GraphQL endpoint, and a server-side rendered web UI with an interactive commit message builder. All convention data is embedded in the binary at build time. A companion CLI tool lets developers browse types and build valid commit messages directly in the terminal. Deployed as a single self-contained static binary.

## Project variables

project_name: gitmessages
project_org: apimgr
internal_name: gitmessages
internal_org: apimgr
app_name: Git Messages API
repo: https://github.com/apimgr/gitmessages
license: MIT
binary: gitmessages
client_binary: gitmessages-cli
owner_token: gitmessages_owner_token_7gT2qA

## Business logic

### Product scope & non-goals

**In scope:**
- Commit type registry: feat, fix, docs, style, refactor, perf, test, chore, ci, build, revert — each with description, emoji, and example message
- Convention definitions: Conventional Commits, Angular commit format
- Commit message templates for common scenarios (feature, bugfix, release, hotfix, docs update)
- Random commit message retrieval
- Interactive commit message builder (select type, scope, description, body)
- Full web frontend (server-side Go templates, dark/light/auto theme, PWA, mobile-first)
- Server pages: `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`, `/server/security`, `/server/preferences`
- Alternate public access over a Tor v3 hidden service when Tor is available, with optional I2P eepsite access according to the shared server specification
- CLI client (`gitmessages-cli`) for shell-pipeline use: `git commit -m "$(gitmessages-cli random)"`
- OpenAPI/Swagger docs at `/api/{api_version}/server/swagger`
- GraphQL at `/graphql`
- MCP over Streamable HTTP at `/api/v1/server/mcp`, with direct aliases `/api/mcp` and `/mcp`, exposing only the public catalog and stateless message builder
- Anonymous owner tokens for resource ownership and MCP access, without user accounts
- Server contact and security-report forms required by the shared server specification; no submitted commit messages or community conventions are accepted

**Non-goals:**
- No user accounts, registration, passwords, or profile login
- No admin web panel (server configured via `server.yml` only)
- No user-submitted messages or community conventions (curated dataset only, updated via releases)
- No paid tiers or rate-limited access tiers
- No git repository integration (read-only reference provider only)

**Product data and third-party integrations:** The catalog is immutable release data embedded in the binary, and the message builder is stateless. The service accepts contact and security reports required by the shared server specification, but never accepts user-submitted commit messages or community conventions. It does not query third-party content or data providers at runtime. Tor and optional I2P provide alternate network access to the same local reference service. Shared operational state, owner tokens, contact reports, and security reports may be persisted as required by AI.md. Email is used for configured contact and security-report delivery; no scheduled catalog jobs run. Shared backup and restore cover applicable operational configuration and state, never user accounts or submitted catalog content. Operational availability controls such as health checks and rate limiting remain applicable.

### Roles & permissions

There are no user accounts or human login roles. Public catalog and web pages are available without a token; owner-token and operator-only routes follow AI.md authentication requirements.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Full read access to all web pages and API endpoints |
| **Anonymous API client (curl/CLI)** | Public catalog access; may use an owner token for MCP and owned resources |
| **Server operator** | Configures server via `server.yml` and uses the operator token for protected operations; no admin web panel |

### Data model & sensitivity

**Commit type record** (embedded at build time, no PII):

| Field | Type | Sensitivity |
|-------|------|-------------|
| `type` | string — type identifier (e.g., `feat`) | Public |
| `emoji` | string — emoji character (e.g., `✨`) | Public |
| `description` | string — human-readable description | Public |
| `example` | string — full example commit message | Public |
| `breaking` | boolean — whether used for breaking changes | Public |

**Message template record** (embedded at build time, no PII):

| Field | Type | Sensitivity |
|-------|------|-------------|
| `name` | string — template identifier | Public |
| `message` | string — commit message text or pattern | Public |
| `category` | string — style category (conventional, emoji, humorous, etc.) | Public |

No PII stored or served.

### Trust boundaries & external services

| Boundary | Trust level | Notes |
|----------|-------------|-------|
| Convention and message dataset (embedded at build) | Fully trusted | Static, compiled into binary |
| Incoming HTTP requests | **Untrusted** | Query and form values, JSON bodies, GraphQL inputs, MCP headers, and origins are validated before use |

No third-party content or data services are called at runtime; Tor and optional I2P are alternate access networks for the local service.

### Threat model & abuse cases

**Primary assets:** service availability, operator credentials, owner tokens, and confidential contact/security report content.

**Attacker/abuser goals:**
- DoS via high-rate requests

**Defenses:**
- Rate limiting on all endpoints
- All query parameters validated against known type and category lists
- No user accounts eliminates credential stuffing and privilege escalation entirely

### Security decisions & exceptions

- **Public catalog access**: intentional. The curated reference remains publicly readable; protected operations follow AI.md.
- **All responses include `Access-Control-Allow-Origin: *`**: intentional. Public data API designed for cross-origin browser use.

### API routes

| Method | Path | Auth | Purpose and response |
|---|---|---|---|
| GET | `/api/v1` | None | API discovery and route index |
| GET | `/api/v1/types` | None | Curated commit types with descriptions, emoji, and examples |
| GET | `/api/v1/conventions` | None | Supported convention definitions |
| GET | `/api/v1/templates` | None | Curated message templates |
| GET | `/api/v1/templates/random` | None | One random template |
| GET | `/api/v1/messages/random` | None | One random example message |
| POST | `/api/v1/messages` | None | Statelessly validate and format the supplied message fields |
| GET | `/api/v1/server/healthz` | None | Public service health and version |
| GET | `/api/v1/server/about` | None | Localized service description |
| GET | `/api/v1/server/help` | None | Localized usage and documentation links |
| GET | `/api/v1/server/privacy` | None | Localized privacy policy |
| GET | `/api/v1/server/terms` | None | Localized terms |
| GET | `/api/v1/server/security` | None | Localized security reporting guidance and links |
| GET | `/api/v1/server/dpo` | None | Privacy contact information |
| GET | `/api/v1/server/preferences` | None | Current request language and theme preference |
| GET, POST | `/api/v1/server/contact` | None | Contact/security report form descriptors and submission flow |
| GET | `/api/v1/server/help/mcp` | None | Localized MCP connection instructions |
| GET | `/api/v1/server/swagger` | None | OpenAPI JSON document |
| GET | `/api/v1/server/metrics[/{service}]` | Service Bearer token | Internal metrics |
| GET | `/api/v1/server/preferences/tokens` | Owner token when managing an existing identity | Owner-token page data |
| POST | `/api/v1/server/preferences/tokens/primary` | None | Issue an owner and primary token |
| POST | `/api/v1/server/mcp`, `/api/mcp`, `/mcp` | Owner or operator Bearer token | MCP JSON-RPC tools for public catalog reads and stateless message building |
| GET, POST | `/graphql`, `/api/graphql`, `/api/v1/server/graphql` | None | Read the catalog or statelessly build a commit message |

All listed API routes accept JSON by default. The GET resources and server pages also support plain text through their `.txt` form or `Accept: text/plain`; the message builder accepts JSON or form-compatible browser input. `/api/swagger` is a compatibility alias for the OpenAPI document. `/.well-known/security.txt` is a public metadata file, not an API route.
