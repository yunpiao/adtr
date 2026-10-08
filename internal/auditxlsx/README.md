# Bounded audit XLSX writer

`Write(ctx, destination, headers, next)` emits one XLSX worksheet named `Audit`
using only Go's standard library. The caller owns field selection, filtering,
authorization, pagination, temporary storage, and publication. This package does
not establish the unresolved F39 dictionary/default-column contract or complete
AD-F-155 product acceptance.

## Contract

- Select 1–8 columns. Empty selection returns `ErrInvalidColumns`; an empty
  result with selected columns produces a workbook containing its header row.
- Each successful iterator call returns one row with exactly the selected column
  count. `io.EOF` ends iteration. An iterator error fails the export without
  treating its accompanying row as successful data.
- Accept at most 100,000 data rows plus the header. The iterator is read one extra
  time to distinguish an exact-limit result from overflow. Overflow fails rather
  than silently exporting only the first 100,000 rows.
- Every cell is an inline string, including headers, numeric-looking text,
  formula-like text starting with `=`, `+`, `-`, or `@`, and URLs. There are no
  formulas, hyperlinks, macros, external relationships, or shared-string table.
- Reject invalid UTF-8 and characters forbidden by XML 1.0. Do not sanitize or
  truncate. A cell may contain at most 32,767 UTF-16 code units; supplementary
  Unicode characters count as two units. This is a conservative Excel-compatible
  bound. Errors identify the worksheet row and column without including content.
- Preserve spaces, tabs, line feeds, XML metacharacters, carriage returns, and
  literal strings matching `_xHHHH_`. The latter two use the standard OOXML
  `ST_Xstring` escaping rules.
- Memory does not grow with the number of rows. XML is buffered in 32 KiB;
  compression buffers and metadata cover only the fixed six ZIP members. The
  iterator can reuse its row slice. Its own database batching/memory belongs to
  the caller.
- Check cancellation before and after iterator calls, between cells, and at the
  destination writer. A blocking iterator or destination must itself support
  cancellation. The caller's writer is never closed by this package.
- On any error, including ZIP finalization or a short write, discard the partial
  output. Only publish after `Write` and the destination's close both succeed.

## Verification

`go test -race -count=1 ./internal/auditxlsx` exercises ZIP members, XML namespaces,
relationships and content types, literal values, header-only output, invalid
columns/cells, exact and excessive row/cell limits, slice reuse while streaming,
cancellation, iterator failures, destination failures, and ZIP-finalization errors.

`go test -run '^$' -bench BenchmarkWrite -benchtime=1x -benchmem ./internal/auditxlsx`
measures generation of 1,000 and 100,000 synthetic eight-column rows to
`io.Discard`. Allocation totals in a benchmark are not peak retained memory or
production capacity acceptance.

A separate read check using the preinstalled openpyxl 3.1.5 successfully opened a
generated workbook, read its `Audit` worksheet and ordinary Unicode/XML values,
and verified that all cells were string-typed with no hyperlinks. That reader
exposes `ST_Xstring` escape spellings in inline-string cells without decoding
them, so carriage-return/escaped-underscore fidelity is checked separately by
the XML contract tests against the specification. Native Excel verification and
maximum-width production workload measurements remain unperformed.

## Format references

- [Microsoft: minimum SpreadsheetML workbook structure](https://learn.microsoft.com/en-us/office/open-xml/spreadsheet/structure-of-a-spreadsheetml-document)
- [Microsoft: inline strings](https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.spreadsheet.inlinestring)
- [Microsoft: ST_Xstring escaping](https://learn.microsoft.com/en-us/openspecs/office_standards/ms-oi29500/d34ae755-c53f-4a44-a363-c6dd3ee018a4)
- [Microsoft: Excel cell limits](https://support.microsoft.com/en-us/excel/excel-specifications-and-limits)
