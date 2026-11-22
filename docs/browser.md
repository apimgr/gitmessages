# Browser application

The site uses server-rendered HTML and remains usable without JavaScript. Pages are responsive, support auto, light, and dark themes, and provide eight embedded languages: English, Spanish, French, German, Italian, Arabic, Chinese, and Japanese. Dark is the default theme. The theme picker saves its selection in a one-year `theme` cookie, which the server uses to render the page theme; auto follows the operating system preference. Choose a language with the page selector or `?lang=CODE`; the preference is saved in a one-year HTTP-only cookie. Without a saved selection, the site negotiates the browser's `Accept-Language` header and falls back to English. Arabic pages use right-to-left layout. Locale JSON is served from the same embedded files at `/locales/{lang}.json`.

- `/` introduces the product.
- `/types` lists all supported commit types and examples.
- `/templates` lists reusable feature, bug-fix, release, hotfix, and documentation templates.
- `/builder` accepts a type, optional scope, description, body, convention, and breaking-change marker. The server validates and renders the resulting message.
- `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, `/server/terms`, and `/server/security` provide service information and security reporting guidance. Their public API representations are under `/api/v1/server/`.
- `/server/preferences` lets visitors save a language and theme in browser cookies. `/api/v1/server/preferences` reports the current request's language and theme.
- `/server/help/mcp` explains the public MCP connection and tools; `/api/v1/server/help/mcp` mirrors that help data.
- `/.well-known/security.txt` publishes the security reporting contact, supported languages, expiry, canonical URL, and policy link.
- `/manifest.json`, `/icon.svg`, and `/sw.js` provide installable PWA metadata and offline navigation fallback.

The PWA service worker is registered by the browser app and caches pages only. It never intercepts API requests or non-GET requests.
