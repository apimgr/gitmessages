# Frontend Rules (PART 26-28, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never hardcode colors — use CSS custom properties
- Never ship a UI that isn't mobile-responsive from day one
- Never default to light-only mode

## CRITICAL - ALWAYS DO
- Support dark/light/auto theme via CSS custom properties
- Designer-level intent on all non-trivial UI — use the `designer` agent
- Keep templates cache-disabled in development for hot reload, cache-enabled
  in production

## Summary
UI conventions follow the standard dark-mode-default, mobile-responsive,
theme-driven approach used across all projects in this template family.

For complete details, see AI.md PART 26-28.
