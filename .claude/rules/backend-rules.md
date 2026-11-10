# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add a database for the immutable embedded catalog or store user data without IDEA.md authorization.
- Never expose secrets, internal paths, raw panic details, or unbounded request bodies.

## CRITICAL - ALWAYS DO

- Bound requests, validate inputs, rate-limit public surfaces, and use structured errors.
- Keep the catalog embedded and handle failures safely.

## Key rules

This product intentionally has no accounts or submissions. For complete details, see AI.md PART 9, PART 10, PART 11, and PART 31.
