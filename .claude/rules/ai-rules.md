# AI Assistant Rules (PART 0, 1)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never modify `AI.md`; it is the implementation source of truth.
- Never guess business behavior, identifiers, or requirements. Read the relevant specification and search existing code first.
- Never claim completion without running the required verification and inspecting its output.
- Never silently ignore errors or leave partial implementations and placeholder work.

## CRITICAL - ALWAYS DO

- Read the complete relevant PART before implementation and follow cross-references.
- Keep `IDEA.md` as the product behavior source and sync README, docs, OpenAPI, and GraphQL with shipped behavior.
- Validate all untrusted input; preserve security controls while improving usability.
- Surface each discovered issue and fix it unless a specification gap requires user clarification.

## Key rules

- Work from evidence, not assumptions; ask only when the specification is silent or ambiguous.
- Run tests and direct linters through the project-required environment before reporting success.
- Keep human-owned task files intact; apply AI.md TODO hygiene to AI-owned backlog files.

For complete details, see AI.md PART 0, 1.
