# Translating Git Messages

Git Messages ships eight embedded locales: English (`en`), Spanish (`es`), French (`fr`), German (`de`), Italian (`it`), Arabic (`ar`), Chinese (`zh`), and Japanese (`ja`). Every locale is shared by the server and CLI. The language selector and `--lang` list are derived from the embedded locale registry.

## Locale files

Locale files live in `src/common/i18n/locales/{lang}.json`. English (`en.json`) is the source for message keys and placeholders. Keep the same message keys and key order in every locale; translate values only. Files use UTF-8 JSON, two-space indentation, no comments, and no trailing commas.

Metadata declares the language code, English and native names, text direction, version, and CLDR plural categories. The registry and locale files must stay aligned. Arabic uses right-to-left layout. The required languages are English, Spanish, French, German, Italian, and Arabic; additional complete locales are welcome.

## Translation rules

- Translate every human-facing value, including web controls, errors, API messages, CLI output, and help text.
- Keep product names, protocol and technology names, code, file names, configuration keys, URLs, and literal values such as `dark` and `auto` unchanged.
- Preserve every placeholder exactly, including its spelling and braces. Its position may move to fit the sentence.
- Use complete, natural sentences rather than fragments that are joined at runtime.
- Use one consistent, neutral register for each language. AI-assisted translations are expected; human corrections take precedence.
- Follow local punctuation and typography. Translations may be longer than English, so review wrapping and overflow.
- Use logical CSS properties for right-to-left support. Add a suitable font stack when a script may not be covered by the system fonts.

## Plurals

Plural messages live under the top-level `plurals` object and are selected with CLDR cardinal categories. Every language must provide all of its declared categories, including `other`. An explicit `zero` form is permitted in any language. `{count}` may be omitted from an individual plural form; all other placeholders must be identical across forms and languages.

Current categories are:

| Language | Categories |
| --- | --- |
| English, Spanish, French, German | `one`, `other` |
| Italian | `one`, `many`, `other` |
| Arabic | `zero`, `one`, `two`, `few`, `many`, `other` |
| Chinese, Japanese | `other` |

## Adding or updating a locale

For a new language, add a complete `{lang}.json` locale with its metadata, direction, and plural categories. The embedded locale registry uses these files, and the parity tests reject missing or extra message keys, empty values, changed placeholders, and missing plural forms. Update the documented language list and count in the README and this page in the same change. No runtime locale files are required; the application serves the embedded JSON at `/locales/{lang}.json` for browser code.

For an existing locale, update only that language's file alongside the English key change. Keep all other locales in sync and update `meta.translators` when translators request credit.

## Translator credits

Contributors who request public credit are listed in the corresponding locale's `meta.translators` field and in this section. No individual contributor names are currently recorded in the shipped locale metadata.

## Review checklist

- Verify keys, placeholders, plural categories, and values with `make test` in the Go toolchain container.
- Review the site at 320, 768, and 1280 pixels in the target language; check for clipping, awkward wrapping, and overflow.
- Check right-to-left pages in Arabic and confirm layout direction and logical spacing.
- Navigate with a keyboard and verify skip links, visible focus, form labels, landmarks, and dynamic status announcements.

## Glossary

Keep recurring terms consistent within each locale. Preserve these technical words and names: Git Messages, Git, commit, Conventional Commits, Angular, API, REST, GraphQL, JSON, CLI, PWA, Tor, I2P, `.onion`, `--lang`, `?lang=`, `dark`, and `auto`.
