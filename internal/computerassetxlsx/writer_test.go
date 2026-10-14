package computerassetxlsx_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/computerassetxlsx"
)

func generate(headers []string, rows [][]string) ([]byte, error) {
	var b bytes.Buffer
	n := 0
	err := computerassetxlsx.Write(context.Background(), &b, headers, func() ([]string, error) {
		if n == len(rows) {
			return nil, io.EOF
		}
		r := rows[n]
		n++
		return r, nil
	})
	return b.Bytes(), err
}
func parts(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			t.Fatal(e)
		}
		d := xml.NewDecoder(bytes.NewReader(b))
		for {
			token, e := d.Token()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			if el, ok := token.(xml.StartElement); ok {
				switch el.Name.Local {
				case "f", "hyperlink", "externalLink", "calcChain":
					t.Fatal("active workbook content")
				}
			}
		}
		out[f.Name] = string(b)
	}
	return out
}
func TestComputerAssetLiteralWorkbookAndNullCells(t *testing.T) {
	rows := [][]string{{"=HYPERLINK(\"https://example.invalid\")", "+1+1", "@SUM(A1)", "\t=cmd()", ""}, {"00123", "<tag>&\"'", "_x0041_", "line\r\n \t😀中文", "0001-01-01T00:00:00Z"}}
	raw, err := generate([]string{"GUID", "SAM", "SID", "DN", "whenCreated"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	p := parts(t, raw)
	if len(p) != 6 || !strings.Contains(p["xl/workbook.xml"], `sheet name="ComputerAssets"`) {
		t.Fatal("incorrect workbook")
	}
	sheet := p["xl/worksheets/sheet1.xml"]
	for _, want := range []string{`t="inlineStr"`, `xml:space="preserve"`, `=HYPERLINK("https://example.invalid")`, `+1+1`, `@SUM(A1)`, `00123`, `&lt;tag&gt;&amp;`, `_x005F_x0041_`, `line_x000D_`, `😀中文`, `0001-01-01T00:00:00Z`} {
		if !strings.Contains(sheet, want) {
			t.Fatalf("literal encoding missing %q", want)
		}
	}
	if strings.Count(sheet, "<row ") != 3 {
		t.Fatal("lost first/last row")
	}
	empty, e := generate([]string{"GUID"}, nil)
	if e != nil || strings.Count(parts(t, empty)["xl/worksheets/sheet1.xml"], "<row ") != 1 {
		t.Fatal("empty must be header-only")
	}
}
func TestComputerAssetXLSXFailClosedBounds(t *testing.T) {
	for _, n := range []int{0, 6} {
		raw, e := generate(make([]string, n), nil)
		if !errors.Is(e, computerassetxlsx.ErrInvalidColumns) || len(raw) != 0 {
			t.Fatal("columns", n, e)
		}
	}
	for _, v := range []string{"secret\x00value", "\x01", "\ufffe", string([]byte{0xff})} {
		_, e := generate([]string{"A"}, [][]string{{v}})
		if !errors.Is(e, computerassetxlsx.ErrInvalidText) || strings.Contains(e.Error(), "secret") {
			t.Fatal("invalid text leaked/accepted", e)
		}
	}
	_, e := generate([]string{"A"}, [][]string{{strings.Repeat("😀", 16384)}})
	if !errors.Is(e, computerassetxlsx.ErrCellTooLong) {
		t.Fatal(e)
	}
	for _, count := range []int{10000, 10001} {
		n := 0
		err := computerassetxlsx.Write(context.Background(), io.Discard, []string{"A"}, func() ([]string, error) {
			if n == count {
				return nil, io.EOF
			}
			n++
			return []string{""}, nil
		})
		if count == 10000 && err != nil || count == 10001 && !errors.Is(err, computerassetxlsx.ErrRowLimit) {
			t.Fatal(count, err)
		}
	}
}
func TestComputerAssetXLSXCancellationAndSinkFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := computerassetxlsx.Write(ctx, io.Discard, []string{"A"}, func() ([]string, error) { t.Fatal("read after cancel"); return nil, io.EOF }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := computerassetxlsx.Write(context.Background(), brokenSink{}, []string{"A"}, func() ([]string, error) { return nil, io.EOF }); !errors.Is(e, io.ErrShortWrite) {
		t.Fatal(e)
	}
}

type brokenSink struct{}

func (brokenSink) Write(p []byte) (int, error) { return len(p) - 1, nil }
