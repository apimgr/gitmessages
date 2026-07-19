# API Rules (PART 21-25, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never accept unvalidated path parameters into filesystem or DB calls
- Never return stack traces or internal errors to clients in production mode
- Never bypass rate limiting, GeoIP, or blocklist checks except via the
  documented allowlist flag

## CRITICAL - ALWAYS DO
- Validate every path segment with `validatePathSegment` (lowercase
  alphanumeric, hyphens, underscores, ≤64 chars)
- Version API routes consistently (`/api/{api_version}/...`)
- Return sanitized errors in production; detailed errors only in development/debug
- Apply `PathSecurityMiddleware` before auth and routing

## Summary
All API surfaces must run through the standard security middleware chain and
never leak internal error detail outside development/debug mode.

For complete details, see AI.md PART 21-25.
