# Frontend Rules (PART 16)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never render server pages from client-only state or require JavaScript for a flow AI.md says must work without it.
- Never emit inline styles, inline executable scripts, unescaped user/config content, or unlabeled controls.
- Never expose credential-bearing pages to shared offline caches.

## CRITICAL - ALWAYS DO

- Use the common SSR page shell, navigation, components, design tokens, and registered help/preferences hubs.
- Ensure every state-changing form has CSRF protection and every page is accessible, responsive, localized, and keyboard-operable.
- Keep page/API content paired and preserve no-JS behavior for server forms.

## Key rules

- Pages and error pages share the required landmarks, one H1, skip links, header, footer, and route-aware metadata.
- Preferences, token delivery, consent, privacy, security, Swagger, and GraphiQL follow their dedicated contracts.
- Service-worker caching is limited to explicitly public, visitor-consistent pages and approved assets.

For complete details, see AI.md PART 16.
