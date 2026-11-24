# Anonymous owner tokens

Git Messages has no user accounts or passwords. An owner is an anonymous identity represented by its primary `own_` token. Tokens link a caller to the same owner across API requests; the public catalog and message builder remain usable without a token.

## Get a primary token

Open `/server/preferences/tokens` and choose **Create token**, or call `POST /api/{api_version}/server/preferences/tokens/primary`. The API returns the new value in `data.owner_token` with status `201`. Keep it private. A valid token already presented to the API receives `409` instead of creating a second owner token.

The browser stores a primary token in an `HttpOnly`, `Secure`, `SameSite=Strict` cookie. To use another browser, restore the identity by pasting the primary token into the token page. A linked token cannot restore an identity. The **Forget saved token** action removes the browser's copy without revoking the credential or deleting resources.

Token actions use POST forms with a Strict double-submit CSRF cookie. Creation and rotation redirect with `303` and show the new token once on the resulting page; reveal also shows a token only for that response. Rotation, creation, rename, and revocation show a toast. Confirmations are provided for destructive actions. Valid owner cookies refresh their configured lifetime when frontend pages use them, and invalid cookies are cleared on the next frontend response. For existing browsers, the previous cookie name remains a read-only fallback indefinitely when the current cookie is absent; a verified legacy cookie is replaced by the current cookie and cleared in the same response. An invalid current cookie never falls back and clears both names. API and MCP authentication ignore browser cookies.

The primary token card shows its final four characters without decrypting the stored credential. Revoking the primary token requires typing those four characters; it deletes the anonymous owner and all primary and linked tokens, clears the browser token cookie, and clears browser storage. The API uses `DELETE /api/{api_version}/server/preferences/tokens/primary` with `{"confirm":"LAST"}` and returns the deleted token and resource counts. This project has no owner-owned resources, so the resource count is zero.

## Create and manage linked tokens

Use one linked token per automation client. A linked token has the owner's resource access, but cannot manage tokens. The tokens page supports reveal, rotation, rename, expiry changes and revocation. Available expiries are never, 7, 30 or 90 days, one year, or a chosen date. A maximum of 25 active linked tokens is allowed by default.

The API mirror requires the primary token as `Authorization: Bearer …` and never reads cookies:

| Method | Route | Purpose |
|---|---|---|
| GET | `/api/{api_version}/server/preferences/tokens` | List masked token metadata |
| POST | `/api/{api_version}/server/preferences/tokens/primary` | Issue an owner token when no valid token is presented |
| POST | `/api/{api_version}/server/preferences/tokens/primary/reveal` | Reveal the primary token |
| POST | `/api/{api_version}/server/preferences/tokens/primary/rotate` | Replace the primary token |
| DELETE | `/api/{api_version}/server/preferences/tokens/primary` | Delete the owner and all tokens; requires the primary token and its last four characters in `confirm` |
| POST | `/api/{api_version}/server/preferences/tokens` | Create a linked token |
| POST | `/api/{api_version}/server/preferences/tokens/{id}/reveal` | Reveal a linked token |
| POST | `/api/{api_version}/server/preferences/tokens/{id}/rotate` | Rotate a linked token |
| PATCH | `/api/{api_version}/server/preferences/tokens/{id}` | Rename or change linked-token expiry |
| DELETE | `/api/{api_version}/server/preferences/tokens/{id}` | Revoke a linked token |

The operator `adm_` token lives in `secrets.yml` and grants operator scope. Owner and linked credentials are stored in SQLite as SHA-256 hashes plus AES-256-GCM encrypted copies, so an owner can reveal them later. Token values never appear in token lists or audit records. Rotating a token invalidates its prior value immediately and leaves other tokens and owner resources unchanged.

## MCP

MCP requires `Authorization: Bearer …` on every request and uses the same operator or owner token as the API. Cookies are ignored. A linked token is recommended for each MCP client. Token issuance and management are not exposed as MCP tools. See [API](api.md#model-context-protocol) for transport details.
