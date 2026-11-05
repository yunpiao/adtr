# Strict offline synthetic learning configuration interchange

Refs [Issue #57](https://github.com/yunpiao/adtr/issues/57), AD-F-096; builds on
the [immutable value contract](learning-config-contract.md). This engineering
slice lets a developer validate, normalize and compare a supplied synthetic
snapshot without writing a Go caller. It does not train a model, manage persisted
configurations, resolve a real domain or accept legacy `machineTime` semantics.
Product acceptance remains **0/209**, and Issue #57 stays open.

## Input contract

`learningconfig.Parse([]byte)` accepts exactly one JSON object of at most
`MaxJSONBytes = 65536` bytes, including all whitespace. The bound caps parser
input and allocations; it is required for this format and deliberately rejects
larger padded documents. Removing it would remove that resource guarantee. The
maximum supported vocabulary fits within this bound in ordinary JSON; extremely
verbose Unicode escaping or padding may exceed it even for otherwise valid values.

Every one of these eight decoded keys is mandatory, exact and case-sensitive:

| Key | Meaning and requirement |
| --- | --- |
| `format` | Must be `synthetic-learning-config-v1`; prevents accidental legacy or future format interpretation. |
| `synthetic` | Must be the JSON boolean `true`; requires explicit synthetic provenance declaration, but does not prove origin. |
| `ruleLabel` | Required opaque synthetic rule label; not a verified rule ID. |
| `dataSourceLabel` | Required opaque synthetic source label; not a connection or data access grant. |
| `domainLabel` | Required opaque synthetic domain label; not live domain authorization. |
| `windowSeconds` | Required integer seconds, 1–2,592,000; no units inference, defaults or numeric coercion. |
| `allowedFields` | Required nonempty caller vocabulary, at most 256 unique ASCII field tokens. |
| `selectedFields` | Required nonempty selection, at most 32 unique members of that exact vocabulary. |

The label/name lengths, ASCII syntax, case sensitivity, set ordering and domain
bounds are exactly those of the original `New(Input)` constructor. `Parse`
always calls it. No parser-only path can construct an unchecked configuration.
Missing keys, `null`, wrong types, unknown keys, duplicates, invalid JSON and
trailing second values fail with an error wrapping `ErrInvalid`. The returned
configuration is invalid on failure, and errors contain fixed field/category
names rather than submitted values or raw decoder errors.

Keys are compared after JSON unescaping: `ruleLabel` and `rule\u004cabel` are the
same key, so providing both is a duplicate. A single escaped ASCII spelling is
accepted. Original bytes need not be canonical: key order, whitespace, ordinary
ASCII escape spelling and set-member order can differ without changing the
canonical value or digest. Case aliases such as `RuleLabel` are rejected.

Raw invalid UTF-8 is rejected before decoding. Every supported string is ASCII;
unpaired UTF-16 surrogate escapes, literal replacement characters, non-ASCII
characters and valid non-ASCII surrogate pairs therefore fail the exact
key/format or constructor checks. Malformed Unicode cannot be accepted after
silent replacement. Arrays accept strings only, stop at their existing count
bounds, and reject nested arrays/objects immediately. There is no generic
recursive object decoder.

Seconds must use integer JSON notation. `1.0`, `1e0`, strings containing digits,
overflow, zero and negatives fail. This avoids silent truncation or guessed
conversion. Those restrictions deliberately exclude alternative spellings of
an integer and fractional windows. The synthetic seconds contract is not a
claim about the old system's numeric `machineTime` field.

## Standalone command

The dedicated command is not linked into the `adtr` server and does not change
the default application build. Build it explicitly or use `go run` with the
repository's Go 1.27.1 toolchain:

```sh
go run ./cmd/adtr-learningconfig canonicalize < examples/synthetic-learning-config-v1.json
go run ./cmd/adtr-learningconfig digest < examples/synthetic-learning-config-v1.json
go run ./cmd/adtr-learningconfig verify LOWERCASE_SHA256 < examples/synthetic-learning-config-v1.json
```

- `canonicalize` writes the original core's canonical snapshot JSON plus one
  newline, so it can be fed back into this command. The SHA-256 covers only the
  canonical JSON bytes, **not** this transport newline or original input bytes.
- `digest` writes one JSON object with `format: synthetic-learning-config-digest-v1`,
  `synthetic: true`, and `sha256` (64 lowercase hexadecimal characters), followed
  by a newline. This output is a digest record, not a configuration snapshot.
- `verify LOWERCASE_SHA256` requires exactly one expected digest argument in that
  lexical form. It compares the validated canonical value, not raw input bytes.
  A match writes the same digest record as `digest` and exits 0; a mismatch writes
  no stdout result, reports a fixed error to stderr and exits 1. This is equality
  checking only, not a signature, authorization, training or quality certificate.

All modes require explicit arguments and reject extras before reading stdin.
The expected digest is necessary only for `verify`; removing it makes the
invocation invalid. Uppercase/whitespace/prefixed digests are not repaired.
Invalid input, read failures and usage errors exit 1. The reader consumes at most
65,537 bytes (bound + one over-limit sentinel) and never intentionally reads the
rest of an oversized stream. It waits for EOF for normal bounded input; there is
no wall-clock deadline on an indefinitely stalled stdin or output destination.

No stdout write is attempted until parsing, constructor validation and optional
digest matching all succeed. Output errors and short writes report a fixed error
and exit 1. A failed destination may already have accepted a prefix, so stdout
is not an atomic file transaction. Shell redirection is controlled by the caller;
the command itself does not open paths, write config files, call a network,
start children, access credentials, or connect to application runtime/storage.
Valid snapshots and digests are intentionally printed, so users should supply
synthetic fixture labels, not confidential data.

## Evidence and remaining gates

Focused tests cover exact canonical bytes, independent SHA-256 expectations,
round trips and mutation isolation, every required key, aliases/duplicates after
unescaping, null/type/shape errors, lexical integer boundaries, field-count and
byte-size boundaries, UTF-8/UTF-16 rejection and sanitized errors. CLI tests also
exercise all three modes, argument validation before input, bounded reads,
read/short-write/write failures, zero stdout on invalid input/digest mismatch,
and actual `main` subprocess exit codes and separate stdout/stderr pipes. Fuzz
seeds and bounded fuzz execution check parser robustness and canonical idempotence.

These are offline engineering checks over synthetic input. They do not supply
PostgreSQL, HTTP, authorization, UI, training/learning, Windows/AD or full product
acceptance evidence. External source review, precise published head CI, target
main compatibility, merge and postmerge checks remain independent gates. No
existing runtime feature, CI threshold or required test is removed by this slice.
