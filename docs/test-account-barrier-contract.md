# Integration-only opened-use acknowledgement barrier

This helper belongs only to disposable synthetic browser tests. Build with
`go build -tags integration ./internal/testaccountbarrier`. The DSN is accepted
only through `ADTR_BARRIER_DATABASE_URL`; the sole argument is
`-control-dir /absolute/disposable/directory`. Keep stdin open for the lifetime
of the harness; EOF, signals, errors and the watchdog close the lock connection.
No application lifecycle data is written by this helper.

## Frozen file protocol, version 1

All writers publish complete JSON atomically by rename in the control directory.
Inputs reject unknown, duplicate, missing or null fields, trailing JSON and files
over 4096 bytes. Ticket is exactly 32 lowercase hexadecimal characters. IDs use
the existing bounded opaque-ID grammar; actorId is a positive canonical decimal
string. The tenant is explicit, including `default` for the browser fixture.

`barrier-ready.json`: `{ "version": 1, "phase": "ready" }`, published only
after separate lock and observer connections have successfully connected.

`barrier-arm.json` has exactly:
`version`, `ticket`, `tenantId`, `actorId`, `domainId`, `accountId`,
`idempotencyKey`. Version is the number 1 and all other values are strings.
The browser writes this before continuing its real intercepted admission request.
Only one arm is accepted per helper process. Proof fields are never accepted.

`barrier-release.json`: `{ "version": 1, "ticket": "<ticket>", "phase": "release" }`.
An explicit matching release rolls back the held transaction. It does not
acknowledge the executor or edit its use/dependencies. Mismatched, malformed or
premature release fails the run and closes any held lock.

`barrier-status.json` always contains exactly these top-level fields:

```json
{
  "version": 1,
  "ticket": "",
  "phase": "ready",
  "sequence": 1,
  "armed": false,
  "ledgerLocked": false,
  "lockHeld": false,
  "fixtureReached": false,
  "fixtureReleased": false,
  "fixtureResponseWritten": false,
  "fixtureHandlerClosed": false,
  "released": false,
  "quiesced": false,
  "errorCode": "",
  "snapshot": null
}
```

Ticket becomes the accepted ticket when armed. Phase is one of `ready`, `armed`,
`ledger_locked`, `fixture_released`, `released`, `quiesced`, `failed`.
Sequence increases on every publication. Booleans are cumulative observations,
except `lockHeld`, which describes the current harness transaction.
`fixtureReleased` records the helper's successful matching release-file write;
`fixtureResponseWritten` and `fixtureHandlerClosed` require fixture markers.
Neither proves executor acknowledgement. `released` means an explicit matching
browser release was accepted and its rollback completed. Watchdog cleanup never
sets `released`. Error codes are fixed sanitized enums, never database messages.

Once the matching opened use is locked, `snapshot` is an object with exactly:

```json
{
  "taskId": "<actual admitted task>",
  "tenantId": "default",
  "actorId": "1",
  "domainId": "<domain>",
  "accountId": "<account>",
  "taskKind": "domain.account_connection_test",
  "taskState": "running",
  "useState": "opened",
  "openedAtPresent": true,
  "openerPresent": true,
  "openerUnchanged": true,
  "quiescedAtPresent": false,
  "quiescenceReason": "",
  "diagnosticCode": "",
  "diagnosticSuccessful": false,
  "accountRevision": "1",
  "accountCredentialRevision": "1",
  "accountDeleted": false,
  "accountCredentialPresent": true,
  "connectionRevision": "2",
  "connectionCredentialGeneration": "2",
  "credentialSource": "operation_account",
  "accountPointerPresent": true,
  "accountPointerMatches": true,
  "customCredentialCount": 0,
  "bindingDependencyCount": 1,
  "exactBindingDependencyCount": 1,
  "taskDependencyCount": 1,
  "exactTaskDependencyCount": 1,
  "totalDependencyCount": 2
}
```

Snapshot is a single independent, non-locking SQL statement matched to the arm's
exact tenant, actor, domain, account, key and task kind. Dependency counts are for
that exact account; binding/task counts count their consumer kind, while exact
counts additionally verify object IDs and pinned credential/binding generations.
The exact binding is compared with the use's original pins. Total also includes
unknown dependency kinds. `customCredentialCount` counts F01 domain credentials.
Revisions are canonical decimal strings. Diagnostic code is the genuine stored
code (`success` for a successful LDAP diagnostic). No payload, credential,
ciphertext, proof, owner, fence, connection string or SQL appears in status.
`openerUnchanged` compares original owner/fence/attempt/opened timestamp internally.

After explicit rollback the observer continues publishing through quiescence and
subsequent pair replacement/removal until stdin closes or the 10-minute lifetime
ends. `quiesced` requires the same use, unchanged opener, `executor_returned`,
quiesced timestamp and zero exact task dependencies; browser assertions additionally
require zero total dependencies after detach.

## Fixture handshake and bounded behavior

On accepting a valid arm the helper atomically writes `fixture-arm.json` with
`{ "version": 1, "ticket": "<ticket>", "operation": "hold_rootdse" }`
before publishing `armed`. Its first lock poll also completes before that status
is published, so admission cannot outrun control setup. It polls the matching genuine opened-use join every
10 ms and acquires only that use row with `FOR UPDATE OF u NOWAIT`. The predicate
requires non-null opener evidence and exactly one result. No task, connection,
account, grant, tenant or advisory lock is acquired by the helper.

Matching `fixture-<ticket>-reached.json` and an acquired row lock may arrive in
either order. Immediately once both exist the helper writes
`fixture-<ticket>-release.json` containing the same version/ticket and
`phase: "release"`. The LDAP fixture's hold is at most 2 seconds. Matching
`released`, `response_written`, `handler_closed`, and `expired` markers use those
fixed phase values. Expiry fails the scenario and releases the row lock.

Connection startup is bounded to 10 seconds and acquisition to 15 seconds; each
query has a 250 ms context. The row lock is held
at most 60 seconds, measured from acquisition. The whole process is bounded to
10 minutes. Every exit path closes both connections and rolls back any held
transaction; cleanup uses its own short context. A failure publishes `failed`
when possible and returns nonzero. Raw errors are never written to stdout/stderr.
The lock connection also sets PostgreSQL's
`idle_in_transaction_session_timeout=60000` as a server-side backstop; the helper
runs no statement on that connection after acquisition until rollback. Cleanup
failures propagate as `rollback_failed` or `connection_close_failed`, including
when stdin EOF or a signal initiated shutdown.

The production cleanup order is schema gate → tenant → task → connection →
account → use (`internal/domains/account_use.go`). The first executor checkpoint
opens the use; later phase authorization and final diagnostic publication call
`currentAccount` but never lock the use (`internal/domains/account_executor.go`).
The harness lock therefore allows genuine LDAP completion and ordinary Finish.
Cleanup may temporarily retain its upstream locks while awaiting the held use;
its ordinary bounded rollback/retry creates the real browser mutation windows.

This proves an opened use whose actual executor return remains unacknowledged.
It does not claim the LDAP network operation remains open during browser detach.
Tests and compilation are recorded separately from actual database/browser runs.

## Test layers

`main_test.go` tests strict control parsing, mismatched identity rejection, both
lock/reached orders, cumulative evidence, refusal to infer acknowledgement from
task success or fixture closure, explicit rollback, and bounded acquisition,
watchdog, expiry, cancellation, changed-arm and observer/cleanup failures.

`database_integration_test.go` requires `ADTR_TEST_DATABASE_URL`; absence fails
instead of skipping. Each test creates an isolated database and calls the actual
`store.Migrate`. The component test uses real account admission and a clearly
scoped synthetic fenced opening executor, followed by the real task Engine
return and production account-use acknowledgement. Only that synthetic test
fixture writes lifecycle data. It validates exact identity/opens-only locking,
independent snapshots, absence of upstream locks, and rollback-enabled genuine
acknowledgement. It does not test credential consumption, the production account
executor, or LDAP wire behavior; the separate genuine browser scenario owns that
evidence. Compiling these tests does not establish database execution success.

## Local validation and remaining execution gate (2026-10-07)

The complete local `make check` passed: 482 DOM tests, default Go race/vet/build,
nine Python contracts and dependency audit with zero reported vulnerabilities.
The final whole-repository integration-tag compilation and vet passed. The LDAP
fixture's actual TLS/BER race suite passed in 6.781 seconds; helper protocol and
coordinator race tests passed. The browser file passes project TypeScript,
formatting and test discovery. Independent source review rechecked fixes for the
unchanged-state409 wait, cleanup-error propagation and premature PASS output.

The two PostgreSQL component tests were attempted and failed at setup because
ADTR_TEST_DATABASE_URL is absent; they were not skipped or reported successful.
Their opening executor is explicitly synthetic and fenced; they test real Engine
return/production acknowledgement and row locking, not production credential
consumption. The separate browser scenario uses the genuine account executor and
LDAP wire, but Docker/PostgreSQL/browser execution has not passed locally. Host
UID/file ownership, binding to fixture ports389/636 and real lock-retry timing
remain actual-runtime gates. No product acceptance is inferred from this source
or compilation evidence.
