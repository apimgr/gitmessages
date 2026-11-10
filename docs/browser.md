# Browser application

The site uses server-rendered HTML and remains usable without JavaScript. Pages are responsive, support auto, light, and dark themes, and provide English, Spanish, Chinese, French, Arabic, German, and Japanese translations. Choose a language with the page selector or `?lang=CODE`; the preference is saved in a one-year HTTP-only cookie. Without a saved selection, the site negotiates the browser's `Accept-Language` header and falls back to English. Arabic pages use right-to-left layout. The selected theme is stored in browser local storage; auto follows the operating system preference.

- `/` introduces the product.
- `/types` lists all supported commit types and examples.
- `/templates` lists reusable feature, bug-fix, release, hotfix, and documentation templates.
- `/builder` accepts a type, optional scope, description, body, convention, and breaking-change marker. The server validates and renders the resulting message.
- `/server/about`, `/server/help`, `/server/healthz`, `/server/privacy`, and `/server/terms` provide service information.
- `/manifest.json`, `/icon.svg`, and `/sw.js` provide installable PWA metadata and offline navigation fallback.

The PWA service worker is registered by the browser app and caches pages only. It never intercepts API requests or non-GET requests.
