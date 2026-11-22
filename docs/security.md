# Security and privacy

Git Messages serves static curated data and does not accept submissions or retain user data. It has no user authentication, accounts, API tokens, or administrative web UI. Builder requests are stateless.

HTTP requests are subject to input validation, request body limits, per-client rate limiting, configured CORS, and browser security headers. The service does not query third-party content or data providers. When Tor is available, the server runs its own dedicated Tor process for onion access; I2P access is optional and uses a local provider. Operators should terminate clearnet TLS at a trusted reverse proxy before public deployment and restrict network access as appropriate.

Security reporting instructions are available at `/server/security`, mirrored at `/api/v1/server/security`, and published as `/.well-known/security.txt`. Reports should use GitHub's private vulnerability reporting channel; do not publish exploit details in a public issue.

The health endpoint reports service version and the availability state of configured overlay services. It may include public onion or I2P hostnames, but never private keys or control credentials. Security reports should be filed through the repository's GitHub security reporting feature.
