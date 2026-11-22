# Contact and security reporting

The public contact form at `/server/contact` accepts only abuse reports about a URL served by this installation, DMCA notices, privacy requests, and coordinated security reports. It is not a support, feature-request, or general-feedback channel. Security reporting instructions are at `/server/security`; repository source-code vulnerabilities should use GitHub's private vulnerability reporting channel.

## Submission flow

The form uses a short-lived single-use form token, a simple arithmetic CAPTCHA, request-origin checks, honeypot handling, rate limits, spam-content checks, and a review step. A submission is delivered only after the sender confirms the review page. Submissions fail closed when the shared database or required security encryption key is unavailable. Email-dependent delivery requires configured SMTP; the server does not queue mail when SMTP is unavailable.

Security reports receive a tracking ID even when SMTP is unavailable. When a project OpenPGP key is installed at `security/pgp.pub.asc`, the report is encrypted to that key before storage and notification. Otherwise, the report uses AES-256-GCM with the configured server security encryption key. Researcher contact details are separately encrypted at rest. Status links are single-use per day and expire after report closure retention. Researchers who provide a public key receive an encrypted acknowledgment when mail delivery is available.

## Routes and mirrors

- `GET /server/contact` renders the type chooser and report forms; `POST` handles review and confirmation.
- `GET, POST /api/v1/server/contact` exposes the same token, CAPTCHA, review, and confirmation flow.
- `/server/security`, `/server/security/policy`, `/server/security/thanks`, `/server/security/report/{id}`, and `/server/dpo` provide the public security, disclosure, status, and privacy-contact pages.
- `/api/v1/server/security`, `/api/v1/server/security/policy`, `/api/v1/server/security/thanks`, and `/api/v1/server/dpo` mirror their public metadata.
- `/.well-known/security.txt` publishes reporting channels and policy links. `/.well-known/pgp-key.asc` serves the public encryption key when publication is enabled.
- `/.well-known/llms.txt` and `/llms.txt` publish the service's public route discovery for automated clients. Well-known files support unauthenticated `GET` and `HEAD` requests.

The security report form requires a rotating `security_id` from `/server/security`. The DPO page links to privacy information and security information and does not link directly to the contact form.

## Configuration

Configure report recipients under `server.contact` and outbound delivery under `server.notifications.email.smtp`. SMTP credentials are sent only after a TLS connection is established. The public contact page can be disabled with `server.pages.contact.enabled`; when disabled, its API is also unavailable. The CAPTCHA provider currently supported by the submission handler is `simple`; unsupported configured providers fail closed.

Operators should set the server encryption key in the secrets configuration and protect the configuration directory. Keep the OpenPGP private key outside the web root; only the public key belongs at `security/pgp.pub.asc`.
