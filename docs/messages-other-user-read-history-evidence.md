# AD-F158 / Issue 50 / DTL036: nonempty other-user read history

## Scope

`TestBulkPreservesNonemptyOtherUserReadHistory` is an integration-tagged PostgreSQL component test. It reuses the messages fixture's independently created database, schema installation, production health task execution and projection, and owned-database cleanup. Both identities are synthetic trusted-adapter inputs. Task and bulk authorization are the existing synthetic fixture authorizers.

This evidence does not exercise two real authenticated browser sessions, persisted login/grant enforcement, the production migration path, or complete DTL036 acceptance. Those boundaries remain separate tests/acceptance work.

## Preconditions and assertions

Each of the three isolated subtests creates real health messages for two different users in the same tenant. The second user has two messages. A committed call to the original `MarkReadTx` reads exactly one; an independent database query then requires a nonempty `message_reads` history with the receipt's exact `first_read_at`. The other message remains unread. No test inserts directly into `message_reads`.

For the current user:

- `selected`: three initial messages, exactly two frozen targets and two actual committed reads
- `all`: three initial messages, exactly three frozen targets and three actual committed reads
- `partial_cancel`: 51 frozen targets, one synchronous worker batch commits exactly 50 reads, cancellation leaves one target unprocessed, and a later worker cycle cannot resume it

A real new health arrival after admission is excluded from every frozen target set. Independent SQL reads compare the complete ordered target set, the exact current-user processed message set, persisted receipt count, and nonzero read timestamps. Progress, state and the immutable admission receipt must agree with these writes. Current-user query counts include the new arrival as unread.

After admission, the late arrival, the first worker batch, cancellation where applicable, and a subsequent terminal worker cycle, the test compares the second user's complete ordered `message_reads` rows (tenant, user, message and exact first-read timestamp) against the committed nonempty baseline. Its complete two-entry query result and all/read/unread counts must remain unchanged. A final unread query must return exactly the original unread message, still without a first-read timestamp.

Cancellation is sequenced after the first batch has returned, without sleeps or timing races. The test deliberately stays within the existing snapshot quota (at most eight snapshots for the second user).

## CI selection and evidence status

The existing `scripts/run_integration.py` non-authentication discovery uses `go list -tags=integration ./...`, excludes only the authentication package, and executes every remaining package with the original `-race -count=1 -tags=integration -timeout=10m` flags. `internal/messages` and the new integration test are therefore selected without another job, filter, deadline, or environment change.

At preparation time, source review, standalone formatting and 16 existing offline integration-runner contracts passed. A mocked discovery containing the authentication and messages packages additionally verified that the actual non-auth command retains `internal/messages` with no `-run` or `-skip` filter; an audit hook prohibited subprocess, signals and sockets during these checks. The PostgreSQL test has not been run locally. Runtime passage must be established by the exact published candidate's CI; this document does not claim a passing run or full requirement completion.
