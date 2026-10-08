# Personal profile and avatar integration (F42)

Refs #61 / AD-F-170. Based on PR #72 `feat/background-tasks` at
`e437919967074bc544cf378d72eab51c37f351cc`; work branch `feat/personal-profile`.
The external backend used Issue #61 as its requirement input. The integrating
owner also retains the original workbook and its 21 field and 14 control mappings. Interface and image limits were confirmed in the [coordination reply](https://github.com/yunpiao/adtr/issues/61#issuecomment-6036920999). They are explicit current product policy, not a claim that legacy semantics or product acceptance have been frozen.

## Scope and integration boundary

New isolated `internal/auth/profile_*.go` files provide a handler, storage,
image validation and tests. Existing authentication helpers are called without
modification. No password/MFA change, third-party authorization (#62), profile
text editing, domain access, task execution, audit subsystem redesign or health
monitoring is included. #62 is deferred until security interfaces are aligned.

The local integration imports the eight isolated files reviewed at PR73 head
`f3618b5563906bb5588cfac8f35eaaf09ce993a0` and adds global migration 11,
production routes, shared schema gates, trusted audit metadata and a browser view.
The first schema gate runs before durable rate checks or authentication; the
second runs after unlocked image processing, before the final live-session check.
Missing or incompatible schema returns 503 without a fallback or request-time DDL.
The avatar success audit projection is refreshed in the same migration.

The browser uses a fixed private endpoint, bounded PNG streaming, an in-memory
bitmap and a canvas. Both 200 and authenticated `avatar_not_found` 404 responses
carry `X-Profile-User-ID` from the server's authenticated actor. The browser checks
it against the profile identity before accepting image or empty state, and refreshes
authentication on a mismatch. No query selector or client-supplied identity is trusted.
Upload response loss offers a fresh read; it never automatically replays a mutation.

## Coordinated API

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
image decoding, after the initial schema gate. A short transaction first validates the live session, CSRF
and self-ID; it commits and releases tenant/user locks before decoding and
encoding. A second transaction then reacquires the locks and revalidates the
same session cookie, user and tenant immediately before the avatar/audit write.
Revocation, expiry or forced password change during image processing therefore
prevents the commit without holding identity locks during CPU-heavy work. Stored image queries always include the authenticated tenant and
user ID. `user_id` has the existing users-table foreign key with cascade delete;
the duplicate tenant column is server-derived (the current users table has no
composite unique key). A corrupt mismatched tenant row is unreadable and cannot
be overwritten by an upsert; the server returns 409 `profile_conflict`.

Explicit coordinated image limits:

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
Integration tests create disposable databases, use real sessions, and cover
persisted profile values, sanitized image replacement, self-only viewer and
cross-tenant behavior, CSRF, forced changes, disabled/expired/revoked sessions,
audit rollback, inconsistent storage, and revocation during unlocked decoding.
Additional integration regressions verify schema rejection before rate/image work,
rechecking schema after decoding, trusted audit metadata, and the actual v10→11
upgrade with existing records preserved. These tests require real PostgreSQL;
tagged compilation alone is not runtime evidence.

## Integrated validation status

The external backend PR73 exact-head CI passed its PostgreSQL tests. That result
does not validate this later integrated tree. Locally, 271 DOM tests, TypeScript,
production build and formatting passed; integrated Go and migration checks are
recorded with the frozen integration commit. The new `--suite profile` CI entry
uses a fresh real database and application server, exercises admin/viewer self-only
uploads, sanitized PNG/JPEG pixels, CSRF, committed-response loss and logout, and
saves `viewer-profile.png` for visual inspection. Real execution of this integrated
suite and inspection of its screenshot remain pending. AD-F-170 remains in progress;
source parity and product acceptance are not claimed.
