package auditxlsx_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yunpiao/adtr/internal/auditxlsx"
)

func rowsIterator(rows [][]string) func() ([]string, error) {
	i := 0
	return func() ([]string, error) {
		if i == len(rows) {
			return nil, io.EOF
		}
		row := rows[i]
		i++
		return row, nil
	}
}

func workbook(t *testing.T, headers []string, rows [][]string) []byte {
	t.Helper()
	var dst bytes.Buffer
	if err := auditxlsx.Write(context.Background(), &dst, headers, rowsIterator(rows)); err != nil {
		t.Fatal(err)
	}
	return dst.Bytes()
}

func parts(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string][]byte)
	for _, file := range z.File {
		if _, duplicate := result[file.Name]; duplicate {
			t.Fatalf("duplicate ZIP member: %s", file.Name)
		}
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(r)
		closeErr := r.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(bytes.NewReader(content))
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid XML in %s: %v", file.Name, err)
			}
			if elem, ok := token.(xml.StartElement); ok {
				switch elem.Name.Local {
				case "f", "hyperlink", "hyperlinks", "externalLink", "externalLinks", "calcChain":
					t.Fatalf("active content %s in %s", elem.Name.Local, file.Name)
				}
			}
		}
		result[file.Name] = content
	}
	return result
}

type sheetXML struct {
	XMLName xml.Name `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main worksheet"`
	Rows    []struct {
		Number int `xml:"r,attr"`
		Cells  []struct {
			Reference string `xml:"r,attr"`
			Type      string `xml:"t,attr"`
			Inline    struct {
				Text struct {
					Space string `xml:"http://www.w3.org/XML/1998/namespace space,attr"`
					Value string `xml:",chardata"`
				} `xml:"t"`
			} `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

var xstringEscape = regexp.MustCompile(`_x([0-9a-fA-F]{4})_`)

// Decode ST_Xstring once. In particular, an escaped underscore must not cause
// a second decoding pass over what was literal audit text.
func decodeXstring(s string) string {
	return xstringEscape.ReplaceAllStringFunc(s, func(escape string) string {
		value, err := strconv.ParseUint(escape[2:6], 16, 16)
		if err != nil {
			panic(err)
		}
		return string(rune(value))
	})
}

func readRows(t *testing.T, content []byte) [][]string {
	t.Helper()
	var sheet sheetXML
	if err := xml.Unmarshal(content, &sheet); err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for r, row := range sheet.Rows {
		if row.Number != r+1 {
			t.Fatalf("row number = %d, want %d", row.Number, r+1)
		}
		values := make([]string, len(row.Cells))
		for c, cell := range row.Cells {
			wantRef := fmt.Sprintf("%c%d", 'A'+c, r+1)
			if cell.Type != "inlineStr" || cell.Reference != wantRef || cell.Inline.Text.Space != "preserve" {
				t.Fatalf("cell %s does not preserve literal text: %+v", wantRef, cell)
			}
			values[c] = decodeXstring(cell.Inline.Text.Value)
		}
		rows = append(rows, values)
	}
	return rows
}

func TestWorkbookStructureAndLiteralValues(t *testing.T) {
	headers := []string{"=header", "+header", "-header", "@header", "Unicode", "Whitespace", "Escapes", "URL"}
	rows := [][]string{
		{"=HYPERLINK(\"https://example.invalid\",\"click\")", "+1+1", "-1+1", "@SUM(A1:A2)", "中文 café 😀 \uFFFD", " \tline\r\nnext\n ", `_x0041_ _x000D_ _x005F_ _x00aF_ _x0041_x0042_`, "https://example.invalid/?a=1&b=<tag>"},
		{"", "00123", "-0001", "1e10", `<>&"' ]]>`, "\t=CMD()", "_xGGGG_ _X0041_", "mailto:synthetic@example.invalid"},
	}
	allParts := parts(t, workbook(t, headers, rows))
	wantNames := []string{"[Content_Types].xml", "_rels/.rels", "docProps/core.xml", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/worksheets/sheet1.xml"}
	if len(allParts) != len(wantNames) {
		t.Fatalf("unexpected ZIP entries: %v", allParts)
	}
	for _, name := range wantNames {
		if len(allParts[name]) == 0 {
			t.Fatalf("missing %s", name)
		}
	}

	var types struct {
		XMLName  xml.Name `xml:"http://schemas.openxmlformats.org/package/2006/content-types Types"`
		Defaults []struct {
			Extension string `xml:"Extension,attr"`
			Type      string `xml:"ContentType,attr"`
		} `xml:"Default"`
		Overrides []struct {
			Name string `xml:"PartName,attr"`
			Type string `xml:"ContentType,attr"`
		} `xml:"Override"`
	}
	if err := xml.Unmarshal(allParts["[Content_Types].xml"], &types); err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]string{
		"/xl/workbook.xml":          "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml",
		"/xl/worksheets/sheet1.xml": "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml",
		"/docProps/core.xml":        "application/vnd.openxmlformats-package.core-properties+xml",
	}
	for _, item := range types.Overrides {
		if wantTypes[item.Name] != item.Type {
			t.Fatalf("unexpected content type %+v", item)
		}
		delete(wantTypes, item.Name)
	}
	if len(wantTypes) != 0 {
		t.Fatalf("missing types: %v", wantTypes)
	}
	defaults := map[string]string{}
	for _, item := range types.Defaults {
		defaults[item.Extension] = item.Type
	}
	if defaults["xml"] != "application/xml" || defaults["rels"] != "application/vnd.openxmlformats-package.relationships+xml" {
		t.Fatalf("incorrect default types: %v", defaults)
	}

	for name, base := range map[string]string{"_rels/.rels": ".", "xl/_rels/workbook.xml.rels": "xl"} {
		var rels struct {
			XMLName xml.Name `xml:"http://schemas.openxmlformats.org/package/2006/relationships Relationships"`
			Items   []struct {
				ID     string `xml:"Id,attr"`
				Type   string `xml:"Type,attr"`
				Target string `xml:"Target,attr"`
				Mode   string `xml:"TargetMode,attr"`
			} `xml:"Relationship"`
		}
		if err := xml.Unmarshal(allParts[name], &rels); err != nil {
			t.Fatal(err)
		}
		if len(rels.Items) == 0 {
			t.Fatalf("no relationships in %s", name)
		}
		for _, rel := range rels.Items {
			if rel.Mode != "" || len(allParts[path.Join(base, rel.Target)]) == 0 {
				t.Fatalf("invalid or external relationship: %+v", rel)
			}
			if name == "xl/_rels/workbook.xml.rels" && (rel.ID != "rId1" || rel.Type != "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" || rel.Target != "worksheets/sheet1.xml") {
				t.Fatalf("incorrect worksheet relationship: %+v", rel)
			}
			if name == "_rels/.rels" {
				want := map[string]string{
					"xl/workbook.xml":   "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument",
					"docProps/core.xml": "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties",
				}
				if want[rel.Target] != rel.Type {
					t.Fatalf("incorrect package relationship: %+v", rel)
				}
			}
		}
	}
	var book struct {
		XMLName xml.Name `xml:"http://schemas.openxmlformats.org/spreadsheetml/2006/main workbook"`
		Sheets  []struct {
			Name         string `xml:"name,attr"`
			ID           int    `xml:"sheetId,attr"`
			Relationship string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(allParts["xl/workbook.xml"], &book); err != nil {
		t.Fatal(err)
	}
	if len(book.Sheets) != 1 || book.Sheets[0].Name != "Audit" || book.Sheets[0].ID != 1 || book.Sheets[0].Relationship != "rId1" {
		t.Fatalf("incorrect workbook sheets: %+v", book)
	}
	wantRows := append([][]string{headers}, rows...)
	if got := readRows(t, allParts["xl/worksheets/sheet1.xml"]); !reflect.DeepEqual(got, wantRows) {
		t.Fatalf("values changed\ngot  %q\nwant %q", got, wantRows)
	}
}

func TestEmptyDataIsHeaderOnly(t *testing.T) {
	headers := []string{"Actor", "Event"}
	got := readRows(t, parts(t, workbook(t, headers, nil))["xl/worksheets/sheet1.xml"])
	if !reflect.DeepEqual(got, [][]string{headers}) {
		t.Fatalf("empty data: %q", got)
	}
}

func TestInvalidColumnsWriteNothing(t *testing.T) {
	for _, headers := range [][]string{nil, {}, {"1", "2", "3", "4", "5", "6", "7", "8", "9"}} {
		var dst bytes.Buffer
		err := auditxlsx.Write(context.Background(), &dst, headers, func() ([]string, error) { t.Fatal("iterator called"); return nil, io.EOF })
		if !errors.Is(err, auditxlsx.ErrInvalidColumns) || dst.Len() != 0 {
			t.Fatalf("headers %q: err=%v, bytes=%d", headers, err, dst.Len())
		}
	}
}

func TestColumnMismatch(t *testing.T) {
	for _, row := range [][]string{nil, {}, {"one"}, {"one", "two", "three"}} {
		err := auditxlsx.Write(context.Background(), io.Discard, []string{"A", "B"}, rowsIterator([][]string{row}))
		if !errors.Is(err, auditxlsx.ErrColumnMismatch) {
			t.Fatalf("row %q: %v", row, err)
		}
	}
}

func TestTextValidationAndLength(t *testing.T) {
	cases := []struct {
		name, value string
		want        error
	}{
		{"nul", "secret\x00value", auditxlsx.ErrInvalidText},
		{"control", "secret\x01value", auditxlsx.ErrInvalidText},
		{"vertical tab", "\x0b", auditxlsx.ErrInvalidText},
		{"form feed", "\x0c", auditxlsx.ErrInvalidText},
		{"invalid UTF8", string([]byte{0xc3, 0x28}), auditxlsx.ErrInvalidText},
		{"surrogate UTF8", string([]byte{0xed, 0xa0, 0x80}), auditxlsx.ErrInvalidText},
		{"noncharacter FFFE", "\ufffe", auditxlsx.ErrInvalidText},
		{"noncharacter FFFF", "\uffff", auditxlsx.ErrInvalidText},
		{"ASCII too long", strings.Repeat("a", 32768), auditxlsx.ErrCellTooLong},
		{"BMP too long", strings.Repeat("中", 32768), auditxlsx.ErrCellTooLong},
		{"supplementary too long", strings.Repeat("😀", 16384), auditxlsx.ErrCellTooLong},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, header := range []bool{true, false} {
				headers, rows := []string{"Field"}, [][]string{{test.value}}
				if header {
					headers, rows = []string{test.value}, nil
				}
				var dst bytes.Buffer
				err := auditxlsx.Write(context.Background(), &dst, headers, rowsIterator(rows))
				if !errors.Is(err, test.want) {
					t.Fatalf("header=%v: %v", header, err)
				}
				if !strings.Contains(err.Error(), "column 1") {
					t.Fatalf("missing cell location: %v", err)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("cell data exposed: %v", err)
				}
				if header && dst.Len() != 0 {
					t.Fatal("invalid header wrote bytes")
				}
			}
		})
	}
	for _, value := range []string{strings.Repeat("a", 32767), strings.Repeat("中", 32767), strings.Repeat("😀", 16383) + "a", "\t\n\r\ufffd\U0010ffff"} {
		got := readRows(t, parts(t, workbook(t, []string{"Value"}, [][]string{{value}}))["xl/worksheets/sheet1.xml"])
		if got[1][0] != value {
			t.Fatal("valid boundary cell changed")
		}
	}
}

func TestRowLimitAndStreamingReuse(t *testing.T) {
	for _, count := range []int{auditxlsx.MaxDataRows, auditxlsx.MaxDataRows + 1} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			var dst bytes.Buffer
			calls := 0
			row := []string{""}
			err := auditxlsx.Write(context.Background(), &dst, []string{"Sequence"}, func() ([]string, error) {
				calls++
				if calls == 50000 && dst.Len() == 0 {
					t.Fatal("all rows buffered before output")
				}
				if calls > count {
					return nil, io.EOF
				}
				row[0] = strconv.Itoa(calls)
				return row, nil
			})
			if count > auditxlsx.MaxDataRows {
				if !errors.Is(err, auditxlsx.ErrRowLimit) || calls != auditxlsx.MaxDataRows+1 {
					t.Fatalf("overflow: err=%v calls=%d", err, calls)
				}
				return
			}
			if err != nil || calls != count+1 {
				t.Fatalf("exact limit: err=%v calls=%d", err, calls)
			}
			got := readRows(t, parts(t, dst.Bytes())["xl/worksheets/sheet1.xml"])
			if len(got) != count+1 {
				t.Fatalf("rows = %d", len(got))
			}
			for _, index := range []int{1, 50000, count} {
				if got[index][0] != strconv.Itoa(index) {
					t.Fatalf("row %d = %q", index, got[index])
				}
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	for _, at := range []string{"before", "next", "eof", "write"} {
		t.Run(at, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var dst bytes.Buffer
			var output io.Writer = &dst
			if at == "before" {
				cancel()
			}
			if at == "write" {
				output = writerFunc(func(p []byte) (int, error) { cancel(); return len(p), nil })
			}
			calls := 0
			err := auditxlsx.Write(ctx, output, []string{"Value"}, func() ([]string, error) {
				calls++
				if at == "before" {
					t.Fatal("called iterator after cancellation")
				}
				if at == "next" || at == "eof" {
					cancel()
				}
				if at == "eof" || calls > 1 {
					return nil, io.EOF
				}
				return []string{"value"}, nil
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			if at == "before" && dst.Len() != 0 {
				t.Fatal("wrote after initial cancellation")
			}
			if at == "next" && calls != 1 {
				t.Fatalf("iterator calls after cancellation: %d", calls)
			}
		})
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	if err := auditxlsx.Write(ctx, io.Discard, []string{"A"}, rowsIterator(nil)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestWriterErrorsIncludingZIPFinalization(t *testing.T) {
	sentinel := errors.New("injected sink failure")
	headers, rows := []string{"Value"}, [][]string{{"=literal"}}
	size := len(workbook(t, headers, rows))
	for _, limit := range []int{0, 1, 100, size - 1} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			remaining := limit
			sink := writerFunc(func(p []byte) (int, error) {
				if len(p) > remaining {
					n := remaining
					remaining = 0
					return n, sentinel
				}
				remaining -= len(p)
				return len(p), nil
			})
			if err := auditxlsx.Write(context.Background(), sink, headers, rowsIterator(rows)); !errors.Is(err, sentinel) {
				t.Fatalf("writer failure lost: %v", err)
			}
		})
	}
	short := writerFunc(func(p []byte) (int, error) { return len(p) - 1, nil })
	if err := auditxlsx.Write(context.Background(), short, headers, rowsIterator(rows)); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %v", err)
	}
}

func TestIteratorFailureDoesNotReadAgain(t *testing.T) {
	sentinel := errors.New("synthetic read failure")
	calls := 0
	err := auditxlsx.Write(context.Background(), io.Discard, []string{"Value"}, func() ([]string, error) {
		calls++
		if calls == 1 {
			return []string{"first"}, nil
		}
		return []string{"must be ignored"}, sentinel
	})
	if !errors.Is(err, sentinel) || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestWriterFailureStopsStreaming(t *testing.T) {
	sentinel := errors.New("stream destination failed")
	calls := 0
	sink := writerFunc(func(p []byte) (int, error) { return 0, sentinel })
	err := auditxlsx.Write(context.Background(), sink, []string{"Value"}, func() ([]string, error) {
		calls++
		if calls > auditxlsx.MaxDataRows {
			return nil, io.EOF
		}
		return []string{strconv.Itoa(calls) + strings.Repeat("synthetic audit payload <>&", 100)}, nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("destination failure lost: %v", err)
	}
	if calls >= auditxlsx.MaxDataRows {
		t.Fatalf("consumed entire source despite failed sink: %d calls", calls)
	}
}

func TestRequiredArguments(t *testing.T) {
	for _, run := range []func() error{
		func() error { return auditxlsx.Write(nil, io.Discard, []string{"A"}, rowsIterator(nil)) },
		func() error { return auditxlsx.Write(context.Background(), nil, []string{"A"}, rowsIterator(nil)) },
		func() error { return auditxlsx.Write(context.Background(), io.Discard, []string{"A"}, nil) },
	} {
		if err := run(); !errors.Is(err, auditxlsx.ErrInvalidArgument) {
			t.Fatalf("required arguments: %v", err)
		}
	}
}

func BenchmarkWrite(b *testing.B) {
	for _, count := range []int{1000, 100000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			headers := []string{"Actor", "IP", "Event", "Result", "Time", "Type", "ID", "Arguments"}
			row := []string{"synthetic-user", "192.0.2.1", "audit.export", "success", "2026-10-07T00:00:00Z", "audit", "", `{"filter":"<synthetic>&value"}`}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				n := 0
				err := auditxlsx.Write(context.Background(), io.Discard, headers, func() ([]string, error) {
					if n == count {
						return nil, io.EOF
					}
					n++
					row[6] = strconv.Itoa(n)
					return row, nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
