# Remaining AI.md implementation work for gitmessages

IDEA.md features are implemented. These shared AI.md requirements are not yet complete:

- [ ] PART 30: finish full i18n and a11y coverage across every human-facing string and API/schema surface.
  - [x] Add shared embedded locale files for English, Spanish, Chinese, French, Arabic, German, and Japanese, with key parity validation.
  - [x] Add `?lang=`, cookie, quality-aware `Accept-Language`, RTL metadata, accessible language selection, and CLI language selection.
  - [x] Localize browser pages, catalog descriptions, REST validation errors, and GraphQL catalog/error responses.
  - [ ] Translate every web/API/GraphQL/CLI string and complete plural/interpolation rules, JavaScript locale loading, and remaining accessibility requirements.
- [ ] PART 31: implement Tor v3 hidden-service support and opt-in I2P eepsite support, including configuration, lifecycle, security, status, and operator documentation.
- [ ] PART 32: add the interactive TUI, config profiles and save rules, shell completions, language support, and documented client/server selection behavior. Complete self-update only after server autodiscovery, binary distribution, and checksum metadata are implemented.
- [ ] PARTs 7–24: complete applicable service/privilege handling, operational metrics, update metadata, and system integration.
  - [x] Record why database, email, GeoIP, scheduled jobs, and backup features do not apply to the immutable, stateless catalog in IDEA.md.
- [ ] PART 28: add isolated Docker/Incus and browser/E2E test runners plus endpoint coverage scripts; current unit tests do not satisfy the shared integration test requirements.
- [ ] Server src/main.go lacks -h/-v short flags and --debug/--color/--lang/--shell shared flags, and uses log.Fatal (exit 1) in many places (found by go-lint, pre-existing)
