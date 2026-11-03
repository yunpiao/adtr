# Synthetic DNS/IP fixed observations and asynchronous XLSX

## Scope and evidence

This implements a synthetic, bounded development slice of Issue #25 AD-F041 and
Issue #30 AD-F053 / EXP009. An actual task Worker creates an actual XLSX artifact;
the observations are explicitly simulated, not evidence from AD, LDAP, live DNS,
or actual-device synchronization. No network collection is performed. It is not
product acceptance, date-filter compatibility, legacy 100,000-row capacity
acceptance, or a claim that the remaining issue scope is complete.

Only trusted code calls `SeedFixtureTx`. There is no arbitrary caller observation
upload API. The producer binds an authorization tenant/domain ID to a normalized
DNS domain string, observation ID, source token, complete canonical record set,
record count and SHA-256 digest. These identifiers are distinct: an authorization
domain ID is never accepted as a caller-supplied DNS filtering boundary.
Observations, operation receipts and artifacts are immutable. Conflicting reseeds
fail; identical reseeds return the original observation.

## Shared selection contract

`dnsipassets.Normalize`, `Select`, `List` and `dnsipxlsx.WriteRecords` remain
unchanged. The list and exporter use the same pinned observation and literal
keyword/family filter. Keyword input casing is preserved in the query, receipt
and task payload; matching is case-insensitive in the existing selector, not
regular-expression or SQL-wildcard matching. Family is empty, `ipv4` or `ipv6`.
DNS names are canonicalized, mapped IPv6 is unmapped, scoped IPv6 is rejected.
Pagination is list-only (offset >= 0, limit 1..1000). Export selects **all** matching
rows, never only the visible page and never truncates to claim success. Empty
selection produces a valid header-only workbook.

The fixed ordered columns are `ID`, `Domain`, `DNS name`, `IP`, `Observation`,
`Source`, `Contract version`, `Simulation`. The existing writer uses inline text,
not formulas or external workbook relationships. Filename is
`SIMULATED-dns-ip-<task UUID>.xlsx`. Task result contains only a bounded manifest
(contract, simulation marker, task ID, pinned source/filter, row count, ordered
columns, byte length, SHA-256 and filename); workbook bytes are in a dedicated
bytea table, never task-result JSON.

## Authorization and execution

The new `simulation.dns_ip.export` kind is owner-scoped, replay-safe and
unschedulable. It does not replace or alter existing registry entries. The
separate simulation DNS/IP feature flag is required to enable this optional
surface; disabled is the safe default. Read/manage permission marks and current
feature/domain/user authorization apply independently of earlier simulation
surfaces. Removing the flag check would expose an explicitly disabled surface;
removing permission, owner or epoch checks would allow stale or cross-owner
access. Enabling the flag does not grant permission or imply production readiness.

Submission requires the source hash, exact filter and operation ID. The receipt
binds actor, tenant, authorization domain, action and full intent digest. A replay
returns the original task, never a second export. Reusing an operation ID with a
different intent fails. Receipt recovery, task reads, cancellation and downloads
all require current engine authorization and matching authorization epoch; a
later regrant does not revive an old task. Audit insertion and receipt/task writes
share the authorized caller transaction, so audit failure rolls back submission.

The existing engine owns leases, fencing, schema checks, checkpoints and terminal
state transitions. `OwnerScoped` is required to prevent cross-owner reads;
`ReplaySafe` enables the engine's existing replay controls;
`CancelDiscardsResult` is required to prevent cancelled output from becoming a
success. The new kind additionally requires its own immutable export receipt,
preventing generic task submission from bypassing business intent validation.

## Artifact publication boundary

Execution renders a complete bounded workbook and returns its manifest. The
`ValidateSuccess` gate revalidates current authorization, the owned business
receipt, pinned immutable source and full filter; re-renders the complete bytes;
and compares the entire manifest. Only then does it insert the immutable
candidate artifact in the engine's Finish transaction. Invalid or forged results
cannot produce a successful export. Ordinary failures before the gate and queued
cancellation do not write an artifact.

The engine performs another authorization check after the gate. If that check
rejects finalization, a candidate may remain stored with a failed task. Such a
candidate is **unpublished**, counts against the finite export quota, and cannot
be downloaded. There is no cleanup trigger and no deletion of prior artifacts,
source observations, receipts or user data. A download requires the same task to
be succeeded under current authorization/epoch, matching task and stored
manifests, owned intent, immutable source, complete byte length and SHA-256, and
re-rendered expected manifest. No queued, running, failed, cancelled, stale-epoch
or partial candidate is downloadable. Durability comes from database rows, not
process-local memory or a temporary file; a fresh process can recover receipts
and verified successful bytes.

## Finite limits and their consequences

These limits are required for this bounded synthetic surface, not capacity
acceptance. Removing a limit permits unbounded storage, response or rendering
work. Exceeding a limit is an explicit failure, never silent truncation.

| Parameter | Value | Purpose and side effect |
| --- | --- | --- |
| Existing record budget | 10,000 records per observation | Bounds validation/sorting/rendering work; larger fixtures fail. |
| Observation count | 8 per tenant/domain | Bounds permanent source storage; additional observations fail. |
| Observation JSON | 8 MiB each | Bounds persisted input and verification work; oversized source fails before write. |
| Export count | 64 per tenant/domain | Counts all immutable export intents, including failed/cancelled/unpublished; later exports may require a separately authorized future retention design. |
| Workbook output | 8 MiB each | Bounded writer rejects overrun before artifact insertion; no partial success. |
| List/receipt response | 1 MiB | Oversized response fails explicitly. |
| Cancellation receipts | At most one per task | Bounds control history while preserving cancellation at export-quota exhaustion. |
| MaxAttempts | 1 | Avoids repeated expensive rendering; new explicit operation is needed after terminal failure and consumes quota. |
| Lease / heartbeat | 30 s / 5 s | Existing engine detects stale ownership and renews live work; removing fencing/renewal can admit stale writes or interrupt valid work. |
| Execution timeout | 10 s | Bounds Worker execution duration; slow rendering fails rather than claiming completion. |
| RetryBase / RetryCap | 1 s / 1 s | Required valid registry timing configuration; no extra attempts with MaxAttempts=1. |

Source storage is at most 64 MiB and workbook storage at most 512 MiB per
 tenant/domain, excluding indexes, metadata, receipts, task history and database
storage overhead. These are hard per-domain logical budgets, not a global
multi-domain storage guarantee. Rendering and validation use bounded whole
observations/workbooks in memory, including temporary copies; this is not a
streaming or constant-memory claim. Rendering occurs at execution, successful
finalization validation and download verification. Context cancellation is
honored by the writer; finalization/download also use their caller contexts.

## Verification inventory

Unit tests cover normalization/provenance/duplicate rejection, all-row and empty
workbook generation, deterministic output, strict payload decoding, bounded
output, registry guards and cancellation. Named PostgreSQL tests exercise an
actual Worker, immutable source/artifact rows, independent authorized
transactions, idempotent recovery, scope isolation, audit rollback, finite quota,
queued cancellation, revoked finalization with an unpublished candidate, and
stale-epoch reads. A missing `ADTR_TEST_DATABASE_URL` is a test failure, not a
skip or a PostgreSQL pass. Compilation and mock tests do not establish database
runtime acceptance; runtime evidence must be recorded separately for the exact
candidate revision.
