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

## Business logic

### Product scope & non-goals

**In scope:**
- Commit type registry: feat, fix, docs, style, refactor, perf, test, chore, ci, build, revert — each with description, emoji, and example message
- Convention definitions: Conventional Commits, Angular commit format
- Commit message templates for common scenarios (feature, bugfix, release, hotfix, docs update)
- Random commit message retrieval
- Interactive commit message builder (select type, scope, description, body)
- Full web frontend (server-side Go templates, dark/light/auto theme, PWA, mobile-first)
- Server pages: `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`
- CLI client (`gitmessages-cli`) for shell-pipeline use: `git commit -m "$(gitmessages-cli random)"`
- OpenAPI/Swagger docs at `/api/{api_version}/server/swagger`
- GraphQL at `/graphql`

**Non-goals:**
- No user accounts, registration, or login of any kind
- No admin web panel (server configured via `server.yml` only)
- No user-submitted messages or community conventions (curated dataset only, updated via releases)
- No paid tiers, no API keys, no rate-limited access tiers
- No git repository integration (read-only reference provider only)

### Roles & permissions

There are no user roles. All endpoints are public and require no authentication.

| Actor | Access |
|-------|--------|
| **Anonymous visitor (browser)** | Full read access to all web pages and API endpoints |
| **Anonymous API client (curl/CLI)** | Full read access to all API endpoints |
| **Server operator** | Configures server via `server.yml` only; no web management interface |

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
| Incoming HTTP requests | **Untrusted** | All query parameters validated |

No external services called at runtime.

### Threat model & abuse cases

**Primary assets:** service availability.

**Attacker/abuser goals:**
- DoS via high-rate requests

**Defenses:**
- Rate limiting on all endpoints
- All query parameters validated against known type and category lists
- No user accounts eliminates credential stuffing and privilege escalation entirely

### Security decisions & exceptions

- **No authentication on any endpoint**: intentional. Public read-only reference API.
- **All responses include `Access-Control-Allow-Origin: *`**: intentional. Public data API designed for cross-origin browser use.
