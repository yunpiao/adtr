// Package auditxlsx writes the bounded, text-only XLSX format used by audit
// exports. It does not select fields, fetch records, or publish artifacts.
package auditxlsx

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const (
	MaxColumns  = 8
	MaxDataRows = 100000
	// MaxCellUTF16Units counts supplementary Unicode characters as two units,
	// conservatively enforcing Excel's 32,767-character cell limit.
	MaxCellUTF16Units = 32767
)

var (
	ErrInvalidArgument = errors.New("auditxlsx: context, writer, and iterator are required")
	ErrInvalidColumns  = errors.New("auditxlsx: select between 1 and 8 columns")
	ErrColumnMismatch  = errors.New("auditxlsx: row column count does not match headers")
	ErrRowLimit        = errors.New("auditxlsx: more than 100000 data rows")
	ErrInvalidText     = errors.New("auditxlsx: invalid UTF-8 or XML 1.0 character")
	ErrCellTooLong     = errors.New("auditxlsx: cell exceeds 32767 UTF-16 code units")
)

// Write streams an XLSX workbook containing one Audit sheet to w. The headers
// form row 1; an empty data set still produces that header row. Selecting no
// columns is an error. Each successful next call must supply exactly as many
// cells as headers. A nil row with io.EOF ends the stream; any row returned with
// an error is ignored. The iterator is called once beyond MaxDataRows to detect
// overflow, and may reuse its row slice after the next call begins.
//
// All cells, including headers, are inline strings. Formula-like text and URLs
// remain text. No formula, hyperlink, macro, or external relationship is added.
// Whitespace and XML metacharacters are preserved, including literal OOXML
// escape sequences. Invalid UTF-8/XML characters and oversized cells produce
// errors, never replacement characters or truncation. Errors identify the
// worksheet row and column without echoing potentially sensitive cell values.
//
// The writer retains no data rows: memory is bounded by ZIP/XML buffers and the
// current row, independently of the total row count. Cancellation is checked
// around iterator calls, between cells, and on writes. The caller must arrange
// cancellation of blocking next/w operations themselves (for example, have the
// iterator's database query use ctx). Write does not close w. On any error, w
// may contain a partial workbook which the caller must discard; publish only
// after Write succeeds and the destination itself has been successfully closed.
func Write(ctx context.Context, w io.Writer, headers []string, next func() ([]string, error)) (err error) {
	if ctx == nil || w == nil || next == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	columns := len(headers)
	if columns < 1 || columns > MaxColumns {
		return ErrInvalidColumns
	}
	if err := validateRow(ctx, headers, 1); err != nil {
		return err
	}

	archive := zip.NewWriter(contextWriter{ctx: ctx, writer: w})
	defer func() {
		if closeErr := archive.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("auditxlsx: close ZIP: %w", closeErr))
		}
	}()
	for _, part := range workbookParts {
		if err := ctx.Err(); err != nil {
			return err
		}
		dst, err := archive.Create(part.name)
		if err != nil {
			return fmt.Errorf("auditxlsx: create %s: %w", part.name, err)
		}
		if _, err := io.WriteString(dst, part.xml); err != nil {
			return fmt.Errorf("auditxlsx: write %s: %w", part.name, err)
		}
	}
	dst, err := archive.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		return fmt.Errorf("auditxlsx: create worksheet: %w", err)
	}
	xml := bufio.NewWriterSize(dst, 32*1024)
	if _, err := xml.WriteString(worksheetStart); err != nil {
		return err
	}
	if err := writeRow(ctx, xml, headers, 1); err != nil {
		return err
	}
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, readErr := next()
		if err := ctx.Err(); err != nil {
			return err
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("auditxlsx: read data row %d: %w", count+1, readErr)
		}
		if count == MaxDataRows {
			return ErrRowLimit
		}
		if len(row) != columns {
			return fmt.Errorf("auditxlsx: worksheet row %d has %d columns, want %d: %w", count+2, len(row), columns, ErrColumnMismatch)
		}
		if err := validateRow(ctx, row, count+2); err != nil {
			return err
		}
		if err := writeRow(ctx, xml, row, count+2); err != nil {
			return err
		}
	}
	if _, err := xml.WriteString("</sheetData></worksheet>"); err != nil {
		return err
	}
	if err := xml.Flush(); err != nil {
		return fmt.Errorf("auditxlsx: flush worksheet: %w", err)
	}
	return ctx.Err()
}

func validateRow(ctx context.Context, cells []string, row int) error {
	for column, cell := range cells {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateText(cell); err != nil {
			return fmt.Errorf("auditxlsx: worksheet row %d column %d: %w", row, column+1, err)
		}
	}
	return nil
}

func validateText(text string) error {
	units := 0
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 1 {
			return ErrInvalidText
		}
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xfffe || r == 0xffff {
			return ErrInvalidText
		}
		units++
		if r > 0xffff {
			units++
		}
		if units > MaxCellUTF16Units {
			return ErrCellTooLong
		}
		text = text[size:]
	}
	return nil
}

func writeRow(ctx context.Context, w *bufio.Writer, cells []string, row int) error {
	rowNumber := strconv.Itoa(row)
	if err := writeStrings(w, `<row r="`, rowNumber, `">`); err != nil {
		return err
	}
	for column, text := range cells {
		if err := ctx.Err(); err != nil {
			return err
		}
		ref := string(rune('A'+column)) + rowNumber
		if err := writeStrings(w, `<c r="`, ref, `" t="inlineStr"><is><t xml:space="preserve">`); err != nil {
			return err
		}
		if err := writeText(w, text); err != nil {
			return err
		}
		if _, err := w.WriteString("</t></is></c>"); err != nil {
			return err
		}
	}
	_, err := w.WriteString("</row>")
	return err
}

func writeStrings(w *bufio.Writer, values ...string) error {
	for _, value := range values {
		if _, err := w.WriteString(value); err != nil {
			return err
		}
	}
	return nil
}

// Escape XML text and ST_Xstring's reserved spellings without constructing a
// second copy of the cell. CR and leading underscores follow MS-OI29500
// section 2.1.1747; tabs and line feeds remain literal XML text.
func writeText(w *bufio.Writer, text string) error {
	start := 0
	for i := 0; i < len(text); i++ {
		var replacement string
		switch text[i] {
		case '&':
			replacement = "&amp;"
		case '<':
			replacement = "&lt;"
		case '>':
			replacement = "&gt;"
		case '\r':
			replacement = "_x000D_"
		case '_':
			if isEscape(text[i:]) {
				replacement = "_x005F_"
			}
		}
		if replacement == "" {
			continue
		}
		if _, err := w.WriteString(text[start:i]); err != nil {
			return err
		}
		if _, err := w.WriteString(replacement); err != nil {
			return err
		}
		start = i + 1
	}
	_, err := w.WriteString(text[start:])
	return err
}

func isEscape(text string) bool {
	if len(text) < 7 || text[1] != 'x' || text[6] != '_' {
		return false
	}
	for _, c := range text[2:6] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = w.ctx.Err()
	}
	return n, err
}

const worksheetStart = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`

var workbookParts = [...]struct {
	name string
	xml  string
}{
	{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/></Types>`},
	{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/></Relationships>`},
	{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Audit</dc:title></cp:coreProperties>`},
	{"xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Audit" sheetId="1" r:id="rId1"/></sheets></workbook>`},
	{"xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`},
}
