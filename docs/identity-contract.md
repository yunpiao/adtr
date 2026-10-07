# Local platform identity contract (F43/F44)

Refs #7 (AD-F-167/168/173), #8 (AD-F-169), and infrastructure #4.
This is a runnable vertical implementation, not acceptance of all 209 functions.
It uses local platform accounts only. No real AD, notification or production identity is connected.

## Source and explicit decisions

Source workbook: 2026-10-03-AD功能清单.xlsx, FLD-2734–2764 and FLD-2850–2857;
UI-0520–0522 and UI-0591–0600. Those rows include reference implementation clues,
not normative permission to preserve insecure behavior or source defects.

- Username: 1–64 ASCII letters/digits/`.`/`_`/`-`, case-insensitive canonical lowercase.
- Password: 12–64 Unicode codepoints (at most 256 UTF-8 bytes), matching the reset
  requirement greater than 11 and the source UI maximum 64. No silent trimming.
- Password storage: randomly salted PBKDF2-HMAC-SHA256, 600,000 iterations, 32-byte output.
  Strength is a display hint: high requires 16 characters and three character classes,
  middle requires two classes, otherwise low; it is not a claim of measured entropy.
- First login, administrative reset and passwords older than 90 days require change.
  Ninety days is this version's explicit product policy, not an inferred source default.
- Sessions: random 256-bit opaque tokens, hashed in PostgreSQL, absolute lifetime 8 hours.
  Password change and MFA enable/disable revoke all existing sessions and rotate the caller.
  Administrative reset revokes the target's sessions and requires change at next login.
- A source `token` result is deliberately delivered as an HttpOnly cookie, never to
  localStorage or JavaScript. Profile fields otherwise preserve the source field names.
- Production cookies are `__Host-adtr_session`, Secure, HttpOnly, SameSite=Strict, Path=/.
  Plain HTTP is limited to explicit development and a loopback trusted origin.
- Every mutation requires exact configured Origin and JSON. Authenticated mutations
  additionally require a session-bound X-CSRF-Token. No wildcard CORS or proxy-IP trust.
- Login failures use generic errors. `mfa_required` is disclosed only after a valid password.
  Durable 15-minute limits: 60 mutating attempts per socket peer IP, 10 login attempts per
  normalized account, 10 sensitive attempts per authenticated account. Reverse proxies
  need a separately reviewed trusted-proxy policy before deployment.
- TOTP: server-generated 160-bit secret, RFC 6238 SHA-1/30 seconds/6 digits, ±1 step.
  Secrets are AES-256-GCM encrypted with per-account associated data. Enrollment expires
  after 10 minutes; confirmation proves the pending secret. Successfully used time steps
  cannot be reused, including enrollment and privileged actions. Codes are never logged.
- MFA disable requires current password and fresh, unused TOTP. Administrative password
  reset requires platform_admin, current password, fresh MFA and same-tenant target;
  it cannot reset the caller or remove target MFA. No unauthenticated recovery endpoint.
- Authentication mutations and audit insert are atomic. Audit rows reject UPDATE/DELETE/TRUNCATE
  with a database trigger. Denied authenticated operations record safe action names with the verified actor and tenant.
  Production still requires separate migration/runtime roles and restricted TRUNCATE/DDL
  rights; a database owner can alter triggers, so this is not a tamper-proof DBA boundary.

## API and UI

Prefix `/api/auth`. GET `/me` returns ID, username, role, priv, mobile, email, remark,
passStrength, hasMfa, needChangePwd, isExpired, passwordNotUpdatedDays, pwdUpdateTm,
csrfToken. GET `/mfa` returns hasMfa. POST endpoints are login, logout, password,
reset-password, mfa/enroll, mfa/confirm and mfa/disable. Errors are `{error: code}`.

Only `/me`, `/password` and `/logout` are available to a forced-change session.
The browser keeps profile/CSRF in memory, cancels stale requests and reloads server
identity when interrupted navigation makes a mutation outcome uncertain.

## Running isolated development

Run `make check` and build the frontend. Set ADTR_DATABASE_URL as already documented,
ADTR_AUTH_KEY to a freshly generated base64-encoded 32-byte random key, and ADTR_ORIGIN
exactly to the browser origin. The same key must be retained for that database because
it encrypts MFA secrets; loss/rotation requires a separately designed recovery flow.
No sample real key or password is shipped. Do not put secrets in shell command arguments.

Run `bin/adtr -mode migrate`. For a fresh owned database only, inject
ADTR_BOOTSTRAP_USERNAME and ADTR_BOOTSTRAP_PASSWORD and run `bin/adtr -mode bootstrap`.
Bootstrap refuses to run once any account exists. Remove those two variables before
starting API. ADTR_WEB_DIR points to the built web/dist directory; production image
contains `/web`. Without ADTR_AUTH_KEY, all authentication routes remain unavailable.
This preserves fail-closed infrastructure-only deployments until configured.

`python3 scripts/test_integration.py` runs real PostgreSQL migration and auth HTTP
contracts in fresh disposable databases. `python3 scripts/test_auth_e2e.py` runs the
actual built browser UI, API and database with generated synthetic credentials.
Install the pinned Playwright Chromium before E2E. Missing Docker/browser is a failure,
not a skip. Unit/DOM tests cannot replace these acceptance layers.

## Remaining release gates

No production deployment, external AD/Windows verification, 209-function completion,
account-management/role-matrix completion or MFA recovery/key-rotation rollout is claimed.
Detailed compatibility against the three reference repositories, capacity targets,
production split DB roles and secret-store provider remain separate release gates.
F43/F44 remain open until the latest commit's real-chain tests and independent review
are verified; broader product and Windows gates remain open even after that slice passes.
