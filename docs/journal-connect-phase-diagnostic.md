# Journal connection failure boundary (CI only)

A journal Append has one existing two-second budget. Its connection step includes admission acquisition and native pgx connection initialization. A deadline error and an expired attempt context identify neither boundary by themselves.

The CI first-failure record adds `connectPhase`, using the same attempt and first WriteFailures increment:

- `admission`: `acquireLifecycle` returned an error before the native connector was called. This includes rejection before waiting and errors after a gate wait; it does not prove the wait consumed the whole budget.
- `native`: admission returned successfully and the native connector returned an error or no connection. This includes pgx initialization, not just network dialing. It does not establish a network, TLS, authentication or server root cause.
- `unknown`: a connect failure lacks a recognized observation.
- `none`: the first failure is another stage, or no failure was recorded.

The phase is returned as a per-call value. No context wrapper, shared latest-result slot, timestamps, elapsed duration, SQL, identity, configuration or original error text is retained or emitted. The original error object and connection lifecycle ordering remain unchanged. The default journal helper uses the original ConnectConfig and does not collect a diagnostic phase.

The existing Wait-after-log parser requires exactly six fields and fixed enums, and rejects impossible stage/phase combinations to an all-unknown observation. This record does not enter the success predicate. All existing zero-error, final Stats, database event/report, business, metadata and cleanup checks remain required. Retry policy and all deadlines are unchanged.

Tests cover pre-ticket cancellation, deterministic gate-wait deadline rejection with zero native calls, native failures with/without a partial handle, nil native result, unchanged wrapper error identity, successful phase reset, first-failure immutability, closed projection, default no-context-sampling, and strict parser handling. The existing tagged dbadmission full-package and operationallogs TestActivityCandidate groups select these tests. Existing real PostgreSQL journal-failure/retry tests remain selected. Local checks are static and mocked only; Go and PostgreSQL execution must be established by the exact candidate CI.

The observed PR181 deadline failure remains unexplained at this granularity until a new actual failure provides this field. This change improves attribution and does not claim to fix it.
