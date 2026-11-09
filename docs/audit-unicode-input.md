# Preserve Unicode audit input at the existing contract limits

Engineering Issue #307, parent #13 / F39. Baseline main
`21dbf9d2fa513a7ebfe006cc5d2ebcd3928cf27f`, tree
`5f466765a3235919980ba7c9146b7bc619e2c307`. Offline frontend correction only.

## Contract and observed defect

`audit-contract.md` defines a maximum of 50 Unicode characters for a literal
keyword and 500 for a visibility-operation reason. `internal/audit/types.go`
checks UTF-8 validity and `utf8.RuneCountInString`; the existing frontend submit
handlers use `validText` with `Array.from(value).length` at those same limits.
These are code-point bounds, not UTF-16-unit or grapheme-cluster counts. Combining
marks and the joiners in emoji sequences each count, without normalization.

The keyword input nevertheless had native `maxLength={50}` and the reason
textarea `maxLength={500}`. Native maxlength measures UTF-16 code units before
submit validation runs. A normal typed keyword of 26 supplementary characters
was silently cut to 25, and the real AuditWorkspace submit sent those 25 instead
of the intended 26. A valid 50-character supplementary keyword and 500-character
supplementary reason could not be entered in full. Over-limit pasted values
could also be silently changed into another, shorter valid request.

Independent discovery loaded the actual baseline component and real imports,
with synthetic HTTP responses, and recorded:

```json
{
  "requestedCodePoints": 26,
  "requestedUTF16": 52,
  "contractValidatorAccepts": true,
  "domCodePoints": 25,
  "domUTF16": 50,
  "requestKeywordCodePoints": 25,
  "requestMatchesDOM": true,
  "requestMatchesIntended": false
}
```

This is jsdom/user-event and request evidence, not a real browser/IME or backend
incident. The discovery report and executable harness are retained in the cloud
`adtr-audit-keyword-unicode-evidence-20261109` directory.

## Minimal correction and consequences

Only those two incorrect native maxlength attributes are removed. They are not
required to enforce the business limits: the unchanged submit validators reject
more than 50/500 code points before issuing a query/mutation, and the backend
retains its own bounds. Keeping the attributes would continue to reject valid
supplementary input and alter pasted text. Merely doubling them would still
truncate some invalid inputs into valid, different requests.

A user may now keep an over-limit draft visible and correct it after the existing
validation message; that is the intentional interaction change. No trimming,
normalization, clipping, new parameter, auto-submit or new persistence is added.
The keyword clear button retains draft-only behavior and focus. Invalid query
drafts do not replace the last applied filter. Reasons retain required/nonblank
and control-character validation; actor proof, permission, original target IDs,
visibility recovery and all backend behavior are unchanged.

## Verification and boundaries

Cached Node 24.19.0 and locked dependencies only, no installation or audit.
Vitest uses `--pool=forks --maxWorkers=2`.

- Original Audit/AuditHistory baseline: 76 passed.
- Initial 17 targeted cases on original production: 14 failed, 3 passed.
- Final new file has 26 cases: keyboard and paste; ASCII, supplementary Han/emoji,
  mixed text, combining characters and ZWJ sequences; exact outgoing keyword and
  hide/restore reason; full invalid drafts rejected without requests; replacement
  selection, clear/focus, StrictMode, correction and last-applied-filter retention.
- Composition events are simulated only, proving the completed field value is
  retained before explicit submit. They do not constitute native IME validation.
- All existing tests are unchanged. Complete suite, final baseline-control,
  independent review, build/type/format checks and non-triggering backup results
  are recorded in #307's evidence comment once verified.

An initial test helper used the wrong Chinese submit-button name; that log is
retained but excluded from the bug evidence. A later TypeScript check rejected
an unsupported `exact` option on a role query. Removing that test-only option
preserved its exact string-name matching; the failed check is retained. Neither
failure required a production-code change or weaker assertion.

No real browser/PostgreSQL, combined-candidate integration, exact-head CI,
production deployment or product acceptance is claimed. No audit, install,
`make check`, PR or CI is triggered. Frozen APT/probe/R4/CI-selection and PR225
are untouched. Issue #307 remains open for downstream gates; product acceptance
remains 0/209.
