# Database admission: lifecycle verification slice

## Status and scope

This slice does not activate a gate in `cmd/adtr`, change the capture guard, or
resolve the observed post-SIGSTOP BEGIN race. The preceding candidate tree is
`ef0a46d24394f4c5659bfc554c6ce7a7c48db624` (PR169). Both computer attempts
rejected a post-stop BEGIN transaction; no further retries were authorized.

The first slice centralizes connection construction and normal Close in
`internal/dbadmission`. Default builds directly delegate to pgx. The optional
`adtr_ci_dbadmission` build tag compiles lifecycle accounting for independent
package tests. Even a tagged application has no installed coordinator: there is
no application installation API. This tag is required only for these tests;
removing it omits the accounting tests and does not change production behavior.
Its runtime cost when tests install it is synchronization and conservative
rejection of new connections after closure.

Migrated source files: tasks/engine.go, tasks/quiescence.go,
schedules/worker.go, messages/projection.go, systemhealth/persistence_store.go,
operationallogs/store.go, domains/account_use.go, domains/directory_use.go,
and store/store.go. The last includes both Ready and migration connection
construction. Any future gate installation MUST isolate migration/bootstrap
process modes. SQL, transaction boundaries, context and Close call order remain
unchanged in these consumers.

## Why connection lifetime

A QueryTracer has no error result and cannot prove zero dispatch on cancellation.
At fixed pgx v5.11.0, Conn.Prepare performs failed-description deallocation before
TracePrepareStart; canceled native Begin can also close the physical connection.
TxStatus is the last received ReadyForQuery, not an atomic in-flight-command
indicator. A DialFunc gate would also intercept cancellation cleanup connections.

Sources: [pgx conn.go](https://github.com/jackc/pgx/blob/v5.11.0/conn.go),
[transactions](https://github.com/jackc/pgx/blob/v5.11.0/tx.go),
[pgconn](https://github.com/jackc/pgx/blob/v5.11.0/pgconn/pgconn.go).
The replacement acquires a permit before the explicit ConnectConfig call and
retains it through every transaction on that physical connection until Close.
It does not interpose on DialFunc or on PostgreSQL commands.

## Local contract

- Admission closure and acquisition serialize under one mutex. A refused call
  returns an error without invoking the connector.
- Startup is counted before connecting. A panic leaves startup pending; a
  connection error poisons the attempt because asynchronous server cleanup is
  not proved complete.
- An open connection remains counted between transactions and during a second
  BEGIN. It never becomes drained merely because a transaction completed.
- The opaque non-embedded wrapper exposes Begin, QueryRow and Close. Native
  pgx.Tx remains available; the typed bounded source checker forbids Tx.Conn
  escapes, aliases and method values in its inspected scope.
- Close only releases a clean permit if the connection was not already closed,
  native Close succeeded, and CleanupDone is closed. An error-triggered native
  close poisons the proof. Repeating wrapper Close does not release twice.
- `locallyDrained` is only local accounting. It is NOT a SEALED signal or proof
  that PostgreSQL has no residual sessions/transactions.

The checker is a bounded source contract, not a complete worker call-graph
proof. `cmd/adtr` reachable closure remains UNPROVEN. Passing these tests cannot
justify enabling a capture gate.

## Validation

Default `go test -race -count=1 ./...` tests delegation and the typed checker.
CI additionally runs tagged unit tests and, inside the existing isolated
PostgreSQL fixture, tagged integration tests. Missing fixture configuration
fails instead of skipping. Tests cover refusal before connector invocation,
startup/panic/error poisoning, acquisition/closure races, second transactions,
repeat Close, native Begin error cleanup, and independent observation of backend
removal. Diagnostic assertions use fixed messages and never print DSNs, SQL,
backend identifiers, configuration values or raw errors.

Local Go/PG execution is restricted in the author's environment. Source review,
standalone gofmt, whitespace validation and offline Python contracts are local
evidence; Go type/race and PostgreSQL runtime results must come from controlled CI.

Local verification: 94 focused Python contracts passed, requirement mapping passed,
and standalone gofmt reported no unformatted files in cmd/internal. A broader
587-test offline run did not pass: 10 failures and 9 errors. Eight errors directly
hit the audit prohibition on subprocess execution. For the other 11 entries, the
two runtime-harness modules were rerun from exact baseline b0f155f4 source under
the same audit: 21 tests, identical 10 failures and one error, with only
socket.__new__ blocked. These 11 failure/error identities exactly match the
candidate run. This establishes the same restricted-environment failures on the
baseline, not a successful full-suite result. Controlled CI must still pass the
full suite. The offline audit was not relaxed.

## Blockers before a second slice

A future design must close all owned connection entry points, drain complete
non-target lifetimes within the original 1.5 seconds, independently confirm no
residual PostgreSQL activity, hold admission across the sole STOP and strict
post-stop validation, and release only after CONT on failure. No timeout reset,
whitelist expansion, forced transaction termination or stop/retry loop is allowed.

The activity callback SQL contains worker identity/cycle, not task identity, and
four slots share it. One existing activity connection does not prove it belongs
to the target export. A task-specific, pre-registered identity mechanism and
complete worker reachable-entry proof need independent review. Initialization,
readiness, recovery, scheduler, maintenance, journal, messages and bulk paths
must be accounted for. Long-lived connections may fail to drain in 1.5 seconds;
that is a capture failure, not grounds to lengthen the deadline.
