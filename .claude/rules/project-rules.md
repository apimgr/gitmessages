# Project Rules (PART 2, 3, 4)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never change the project's MIT license or omit attribution for shipped third-party code and data.
- Never commit runtime data, secrets, build output, or files outside the prescribed project layout.
- Never derive stable project identifiers from a convenient but unverified path when IDEA.md or the Git remote provides them.

## CRITICAL - ALWAYS DO

- Keep required root files, source, Docker assets, and CI files in their specified locations.
- Use the stable IDEA.md project variables for on-disk paths and identifiers.
- Verify embedded dataset provenance, redistribution terms, and required attribution before shipping it.

## Key rules

- `LICENSE.md` is required and must include dependencies and embedded-data attribution.
- Resolve paths from the repository root; runtime files belong under configured runtime directories.
- Keep README.md current and follow the required section order, badges, disclaimer, and URL rules.

For complete details, see AI.md PART 2, 3, 4.
