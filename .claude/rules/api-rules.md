# API Rules (PART 13, 14, 15)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never publish unversioned REST routes except explicitly required direct aliases.
- Never let OpenAPI or GraphQL drift from the implemented API.

## CRITICAL - ALWAYS DO

- Use `/api/v1` for REST resources and the shared response/error envelope.
- Keep health, OpenAPI, and GraphQL public; recommend TLS termination for deployment.

## Key rules

The product catalog is public and read-only. For complete details, see AI.md PART 13, PART 14, and PART 15.
