# Backend Rules (PART 9, 10, 11, 31)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never add a database for the immutable embedded catalog. Persist only operational, owner-token, contact, and security-report data required by AI.md.
- Never expose secrets, internal paths, raw panic details, or unbounded request bodies.

## CRITICAL - ALWAYS DO

- Bound requests, validate inputs, rate-limit public surfaces, and use structured errors.
- Keep the catalog embedded and handle failures safely.

## Key rules

This product has no user accounts or submitted catalog content. Shared AI.md owner-token and contact/security-report features apply. For complete details, see AI.md PART 9, PART 10, PART 11, and PART 31.
