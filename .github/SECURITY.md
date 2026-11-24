# Security policy

## Supported versions

Security fixes are provided for the latest published release. Upgrade to the
latest release before reporting an issue against an older version.

## Reporting a vulnerability

Use GitHub private vulnerability reporting at
https://github.com/apimgr/gitmessages/security/advisories/new (Repository
**Security** tab → **Report a vulnerability**). Do not report vulnerabilities
through public issues, discussions, or pull requests. Mirrors should direct
reports to the GitHub origin repository.

Include the affected version or commit, impact, reproduction steps, and a
possible mitigation. The maintainers will acknowledge the report, investigate
privately, coordinate a fix and release, and agree on a public disclosure date
with the reporter. The target is to acknowledge reports within 48 hours and
release a fix within 90 days, depending on severity and the time needed to
validate a safe correction. Please allow maintainers time to verify and
address the report before disclosure.

The application exposes `/server/security`, `/.well-known/security.txt`, and
`/server/contact?security_id=...` for deployed-instance security reporting.
Those instance routes are separate from source-code vulnerability reporting;
GitHub private reporting remains the primary channel for this repository.
