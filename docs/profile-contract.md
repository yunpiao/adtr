# Personal profile and avatar slice (proposed F42 contract)

Refs #61 / AD-F-170. Based on PR #72 `feat/background-tasks` at
`e437919967074bc544cf378d72eab51c37f351cc`; work branch `feat/personal-profile`.
The Issue, not the unavailable original workbook, is this slice's requirement
input. Interface and image limits below are proposals awaiting coordination,
not a claim that legacy semantics or product acceptance have been frozen.

## Scope and integration boundary

New isolated `internal/auth/profile_*.go` files provide a handler, storage,
image validation and tests. Existing authentication helpers are called without
modification. No password/MFA change, third-party authorization (#62), profile
text editing, domain access, task execution, audit subsystem redesign or health
monitoring is included. #62 is deferred until security interfaces are aligned.

The integrating owner, not this PR, must:

1. Allocate the next global migration version and execute `auth.ProfileSchema`
   inside the existing locked migration transaction. Update schema compatibility
   and migration tests coherently. The fragment is deliberately unregistered,
   has no independent version and is not a repeatable runtime migration.
2. Mount `http.HandlerFunc(authService.ServeProfileHTTP)` at `/api/profile/`
   after the coordinated migration and readiness gates. This PR does not mount
   it or modify `cmd/adtr`, `internal/store`, or any existing auth/registry file.
3. Integrate a browser view and upload flow, if desired, and verify the actual
   browser-to-API-to-database chain. This API-only slice cannot establish page
   state or UI acceptance. It does not modify the application shell or CI.

Until those integration steps, the running application exposes no new endpoint.
Missing avatar storage returns a generic 500; there is no silent fallback,
DDL-at-request-time, fake success or invented avatar.

## Proposed API

- `GET /api/profile/me`: current user's persisted profile; no query parameters.
- `GET /api/profile/avatar`: current user's sanitized PNG; absent image returns
  404 `avatar_not_found`. No public avatar URLs or target-ID read endpoint.
- `POST /api/profile/avatar`: `application/json` with exactly `userId` (positive
  int64 JSON integer, equal to authenticated user) and `file` (canonical padded
  standard base64 image bytes). Returns `{ "result": "success" }` only after
  the storage and success audit transaction commits. Failure is `{ "error":
  code }` with a non-2xx status, not a success-shaped `failed` value.

Every request rejects query parameters, including attempted tenant/user scope
selection. JSON duplicates, unknown/case-folded keys, missing/null fields,
trailing documents, invalid UTF-8 and bodies over 3 MiB are rejected. A valid
userId does not authorize another person's record, even for a platform admin.
No upload URLs, data URIs, filenames, filesystem paths or client MIME claims
are accepted. Duplicate repeated uploads atomically replace the same single
record and each accepted mutation records its own audit event.

`AccessUser` is reused for consistent existing role/profile semantics. The
result contains `ID`, `username`, `passStrength`, `role`, `priv`, `mobile`,
`email`, `remark`, `createTm`, `hasMfa`, `avatar`, `pwdUpdateTm`, `address`,
`realName`, `department`, `post`, `roleID`, `roleName`, and existing `disabled`.
Timestamps are UTC. `avatar` is empty before upload and `/api/profile/avatar`
afterward. Profile text is read from actual user records with no modification.
Role IDs remain the existing platform_admin/viewer/custom-role model; this
slice does not claim compatibility with source mgr/dev/ops/sec names.
Password hashes, MFA secrets, authentication tokens and CSRF values are never
part of this response.

## Security and resource contract

The handler reuses `authenticateAccess`: trusted server session identity,
tenant-first/user-second serialization, post-lock session recheck, disabled
user and forced-password-change rejection, and constant-time CSRF validation
for writes. Every upload additionally requires the exact configured Origin.
An authenticated ordinary viewer can read/update only their own avatar;
management grants are not required and grant delegation is not involved.
No new permissions are registered or inherited from `userId`.

POST uses the existing durable IP limiter (60 writes/15 minutes, shared bucket)
and a profile-account bucket (30 uploads/15 minutes). Both are checked before
image decoding; decoding occurs only after live authentication and self-ID
validation. Stored image queries always include the authenticated tenant and
user ID. `user_id` has the existing users-table foreign key with cascade delete;
the duplicate tenant column is server-derived (the current users table has no
composite unique key). A corrupt mismatched tenant row is unreadable and cannot
be overwritten by an upsert; the server returns 409 `profile_conflict`.

Proposed explicit image limits:

- Canonical padded standard base64; no ignored whitespace or alternate encodings
- Decoded upload 1 byte–2 MiB, PNG or JPEG only
- Width and height each 1–1024 pixels, at most 1,048,576 pixels
- DecodeConfig before full Decode; decoded dimensions/format must agree
- Re-encode as PNG to strip metadata and appended payloads
- Encoded stored output at most 4 MiB, also constrained in PostgreSQL

SVG/GIF, animated payloads not represented by the accepted decoders, arbitrary
active content and malformed/truncated images are rejected or stripped by
canonical re-encoding. A decoded image has a bounded raster allocation; input
and output limits are independently enforced. No image content or user-supplied
filename is logged or included in audit. `profile_avatar_update` uses the
existing audit function in the same transaction as the replacement; audit
failure rolls back the image. Errors reuse the existing safe audit-failure path.

All responses use `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`
and `Cross-Origin-Resource-Policy: same-origin`. Successful binary responses
use fixed `image/png` and `inline; filename="avatar.png"`. They do not redirect,
set a credential or grant persistent access. Reads require the session cookie.

## Validation and remaining gates

Run with the repository's pinned Go/Node toolchains:

```sh
make check
go test -race -count=1 ./internal/auth -run 'TestProfile'
go test -tags=integration -run '^$' ./internal/auth
# Requires isolated real PostgreSQL with CREATE DATABASE rights:
go test -race -count=1 -tags=integration ./internal/auth -run '^TestProfile'
# Existing full Docker-backed suite includes ./... and thus the profile tests:
python3 scripts/test_integration.py
```

Unit/HTTP tests cover JSON strictness, base64/type/size/dimension validation,
real PNG/JPEG pixels, metadata stripping, malformed body and routing boundaries.
Integration tests create disposable databases, explicitly apply the otherwise
unregistered fragment, reuse real session HTTP login, and check persisted
profile values, missing/replaced bytes, self-only viewer/cross-tenant behavior,
CSRF, forced change, disabled/expired/revoked sessions, failed-audit rollback,
retry, inconsistent tenant storage and concurrent revocation while real GET/POST
requests wait on the shared tenant lock. They are API/handler/database tests,
not browser tests or proof the application's production route is wired.

Baseline PR72 `make check` passed locally: Go race/vet/build, requirement and
Python contracts, 94 DOM tests, frontend typecheck/build, zero audit findings.
The local executor has no Docker; the Docker integration script failed with
`FileNotFoundError: docker`, not a pass or skip. Final-code and exact-commit CI
results are recorded in the PR separately. A green unrelated browser suite is
not profile browser acceptance. Source-system behavior, UI controls, global
migration/version integration, authenticated browser upload/recovery, live
revocation concurrency in the integrated application, capacity and original
field semantics remain release gates. No Issue closure, merge, production
deployment, Windows/AD acceptance or 1:1 feature completion is claimed.
