# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never create a web administration panel or runtime settings mutation API.
- Never trust unvalidated paths, forwarded headers, or environment values.

## CRITICAL - ALWAYS DO

- Keep server settings in `server.yml`; document defaults and environment/flag precedence.
- Apply safe production defaults and make development mode explicit.

## Key rules

IDEA.md prohibits runtime administration. For complete details, see AI.md PART 5, PART 6, and PART 12.
