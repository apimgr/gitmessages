# Gitmessages AI alignment

Implement and verify every requirement in AI.md. AI.md remains read-only and is the implementation source of truth; IDEA.md supplies product behavior. Keep browser, REST, GraphQL, CLI, OpenAPI, MCP, and documentation aligned. Remove each item in the same successful commit that completes it, per AI.md TODO Hygiene.

- [ ] PART 11: implement the `app_secrets` version/revocation schema, atomic startup initialization for all generated DB secrets, automatic key rotation and grace handling, and make CSRF/token-view values use the required signing secrets.
- [ ] PART 21: implement the `BKUP1` authenticated encryption header, password/key-id metadata, and incremental base metadata; finish backup contents/restore/retention behavior against the current contract.
- [ ] PART 5/8/23: implement staged update installation with privilege captured at startup and a privileged restart phase that independently fetches the official checksum over HTTPS.
- [ ] PART 30/32: align language selection across server and CLI with flag/config/environment/system-locale precedence and verify all server pages and their no-JavaScript paths against the current UI contracts.
- [ ] Current AI.md delta: complete a fresh cross-part code, docs, CI/CD, and server-page audit; fix every discovered drift, synchronize documentation, and run the complete required validation and CI checks.
