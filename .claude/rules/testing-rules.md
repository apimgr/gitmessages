# Testing, Documentation, and Accessibility Rules (PART 28, 29, 30)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run Go commands on the host; use the required Go container or approved Incus test environment.
- Never claim tests, builds, lint, docs, or integration checks passed without actual successful output.
- Never ship a locale with missing keys, placeholders, plural categories, or unsupported documentation claims.

## CRITICAL - ALWAYS DO

- Keep tests beside Go source and executable integration checks under `tests/`.
- Document all user/operator/integrator-visible behavior in the ReadTheDocs set.
- Translate every human-facing string and meet the documented locale parity, accessibility, and responsive requirements.
- Run the test matrix and security checks in the exact environments prescribed by AI.md.

## Key rules

- CI runs explicit commands against actual project features; local checks do not replace CI verification.
- Browser checks cover server-rendered/no-JS behavior, accessibility, supported locales, themes, and supported widths.
- Keep `README.md`, docs, API descriptions, and implementation synchronized.

For complete details, see AI.md PART 28, 29, 30.
