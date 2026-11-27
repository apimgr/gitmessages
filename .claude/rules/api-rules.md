# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never expose private operational state, secrets, internal paths, or credentials through public health/API responses.
- Never add an undocumented route alias or allow cookie credentials to authorize API requests.
- Never bypass certificate validation or construct externally visible URLs from untrusted host headers.

## CRITICAL - ALWAYS DO

- Keep REST, OpenAPI, GraphQL, MCP, browser pages, and docs aligned to the same route and data contracts.
- Use the canonical versioned API prefix and documented direct aliases; return the specified JSON/text/error formats.
- Build absolute URLs from the trusted request/configuration resolution rules and enforce TLS policy.

## Key rules

- Health reports public-safe operational status; detailed diagnostics require explicit debug mode and operator auth.
- Validate request shape, method, content type, size, auth scope, and rate limit before processing.
- API errors preserve their specified HTTP status and safe translated message.

For complete details, see AI.md PART 13, 14, 15.
