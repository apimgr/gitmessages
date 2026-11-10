# Security and privacy

Git Messages serves static curated data and does not accept submissions or retain user data. It has no user authentication, accounts, API tokens, or administrative web UI. Builder requests are stateless.

HTTP requests are subject to input validation, request body limits, per-client rate limiting, CORS for the public API, and standard browser security headers. The service does not call third-party services at runtime. Operators should terminate TLS at a trusted reverse proxy before public deployment and restrict network access as appropriate.

The health endpoint discloses the service name and version only. Security reports should be filed through the repository's GitHub security reporting feature.
