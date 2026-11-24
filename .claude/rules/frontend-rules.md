# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never require JavaScript for navigation, reading references, or building messages.
- Never render unescaped catalog or request content as HTML.

## CRITICAL - ALWAYS DO

- Use responsive server-rendered HTML, keyboard-visible focus, and high contrast themes.
- Keep PWA caching same-origin, GET-only, and safe on network/cache failure.
- Every page uses the shared shell, one page title, translated controls, visible focus, mobile-safe long strings, and complete light/dark/auto plus RTL layouts.
- Exercise every server page, including token, preference-sync, DPO, and security pages, in browser E2E coverage.

## Key rules

HTML forms are the working baseline; JavaScript only enhances. For complete details, see AI.md PART 16.
