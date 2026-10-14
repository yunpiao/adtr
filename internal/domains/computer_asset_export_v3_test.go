package domains

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

func validComputerExportPayload() string {
	return `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"2","observationId":"observed","screenColumns":[]}`
}
func TestComputerAssetExportV3CanonicalColumnsAndClosedPayload(t *testing.T) {
	raw, err := ValidateComputerAssetExportV3Payload([]byte(validComputerExportPayload()))
	if err != nil {
		t.Fatal(err)
	}
	var p ComputerAssetExportV3Payload
	if json.Unmarshal(raw, &p) != nil || !reflect.DeepEqual(p.ScreenColumns, computerAssetExportColumns) || p.Search != "" {
		t.Fatal("defaults", string(raw))
	}
	for _, input := range []string{`{}`, `null`, strings.Replace(validComputerExportPayload(), `[]`, `null`, 1), strings.Replace(validComputerExportPayload(), `[]`, `["guid","guid"]`, 1), strings.Replace(validComputerExportPayload(), `[]`, `["status"]`, 1), strings.Replace(validComputerExportPayload(), `"observed"`, `""`, 1), strings.Replace(validComputerExportPayload(), `"1"`, `1`, 1), strings.Replace(validComputerExportPayload(), `}`, `,"pageSize":10}`, 1), strings.Replace(validComputerExportPayload(), `}`, `,"domainId":"two"}`, 1), strings.Replace(validComputerExportPayload(), `}`, `,"search":"\ud800"}`, 1), strings.Replace(validComputerExportPayload(), `}`, `,"search":"\udc00"}`, 1), validComputerExportPayload() + `{}`} {
		if _, err := ValidateComputerAssetExportV3Payload([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, search := range []string{`\ud83d\ude00`, `\\ud800`, `\u0000`} {
		input := strings.Replace(validComputerExportPayload(), `}`, `,"search":"`+search+`"}`, 1)
		if _, err := ValidateComputerAssetExportV3Payload([]byte(input)); err != nil {
			t.Fatal("valid lossless search", search, err)
		}
	}
}
func TestComputerAssetExportKindIsIsolatedAndGated(t *testing.T) {
	k := New(nil).ComputerAssetExportV3Kind()
	if k.Name != ComputerAssetExportV3KindName || k.Platform || !k.OwnerScoped || !k.ReplaySafe || !k.CancelDiscardsResult || k.Schedulable || k.MaxAttempts != 3 || k.ValidateSuccess == nil {
		t.Fatal("unsafe policy")
	}
	if _, err := tasks.NewRegistry(k); err != nil {
		t.Fatal(err)
	}
	if out := k.Execute(context.Background(), tasks.Execution{Task: tasks.Task{Payload: []byte(validComputerExportPayload())}}); out.Code != "fencing_required" || out.State != tasks.Failed {
		t.Fatal("unguarded execution")
	}
}
func TestComputerAssetArtifactResultRejectsMalformedMetadata(t *testing.T) {
	base := `{"artifactToken":1,"rowCount":0,"byteCount":100,"sha256":"` + strings.Repeat("a", 64) + `"}`
	if _, err := decodeComputerAssetExportResult([]byte(base)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(base, `"rowCount":0,`, "", 1), strings.Replace(base, `"artifactToken":1`, `"artifactToken":0`, 1), strings.Replace(base, `"rowCount":0`, `"rowCount":10001`, 1), strings.Replace(base, `"byteCount":100`, `"byteCount":33554433`, 1), strings.Replace(base, strings.Repeat("a", 64), strings.Repeat("A", 64), 1), strings.Replace(base, `}`, `,"extra":1}`, 1), strings.Replace(base, `}`, `,"rowCount":0}`, 1)} {
		if _, err := decodeComputerAssetExportResult([]byte(bad)); err == nil {
			t.Fatal("malformed metadata accepted")
		}
	}
}

func TestComputerExportPredicateAndOrderExactlyMatchEveryListPage(t *testing.T) {
	o := computerObservation()
	defer o.Discard()
	for _, search := range []string{"", "computer-", "computer-02", " computer", "mail-only", "S-1-5", "CN="} {
		for _, order := range []string{"", "asc", "desc"} {
			f := computerFilter()
			f.Search, f.CreatTm, f.ObservationID = search, order, "obs"
			rows, count, err := computerAssetsV3Rows(context.Background(), o, f)
			if err != nil || count != 23 {
				t.Fatal(count, err)
			}
			var all []string
			for page := 1; ; page++ {
				f.PageIdx = page
				got, err := computerAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
				if err != nil || got.Page.Total != len(rows) {
					t.Fatal(got, err)
				}
				for _, r := range got.List {
					all = append(all, r.GUID)
				}
				if page >= got.Page.Pages {
					break
				}
			}
			if len(all) != len(rows) {
				t.Fatal("page/export counts differ")
			}
			for i, row := range rows {
				if all[i] != row.GUID {
					t.Fatal("page/export order differs")
				}
			}
		}
	}
}
func TestComputerExportRejectsUserFiltersAndPreservesRawSearchAndColumns(t *testing.T) {
	for _, field := range []string{"sAMAccountName", "startTm", "endTm", "mail", "pageIdx", "pageSize", "overviewType", "Search", "DomainID", "ScreenColumns"} {
		raw := strings.Replace(validComputerExportPayload(), "}", `,"`+field+`":"x"}`, 1)
		if _, err := ValidateComputerAssetExportV3Payload([]byte(raw)); err == nil {
			t.Fatal("accepted user/unsupported filter", field)
		}
	}
	for _, search := range []string{strings.Repeat("😀", 26), strings.Repeat("a", 51)} {
		raw := strings.Replace(validComputerExportPayload(), "}", `,"search":"`+search+`"}`, 1)
		if _, err := ValidateComputerAssetExportV3Payload([]byte(raw)); err == nil {
			t.Fatal("accepted oversized search")
		}
	}
	raw := strings.Replace(validComputerExportPayload(), `[]`, `["dn","guid"]`, 1)
	raw = strings.Replace(raw, "}", `,"search":"  x ","creatTm":"desc"}`, 1)
	canonical, err := ValidateComputerAssetExportV3Payload([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var p ComputerAssetExportV3Payload
	if json.Unmarshal(canonical, &p) != nil || p.Search != "  x " || !reflect.DeepEqual(p.ScreenColumns, []string{"dn", "guid"}) {
		t.Fatal("raw search/columns changed")
	}
}

func TestComputerExportProjectionPreservesSelectedOrderAndMissingScalars(t *testing.T) {
	row := directoryassets.PublicObjectV3{PublicObjectV2: directoryassets.PublicObjectV2{Object: directoryassets.Object{GUID: "id", DN: "CN=x"}}}
	if got := computerAssetExportHeaders([]string{"dn", "guid", "sAMAccountName", "sid", "whenCreated"}); !reflect.DeepEqual(got, []string{"DN", "GUID", "SAM", "SID", "whenCreated"}) {
		t.Fatal(got)
	}
	if got := computerAssetExportCells(row, []string{"dn", "guid", "sAMAccountName", "sid", "whenCreated"}); !reflect.DeepEqual(got, []string{"CN=x", "id", "", "", ""}) {
		t.Fatal(got)
	}
	if ComputerAssetExportV3Filename("task") != "computer-assets-v3-task.xlsx" {
		t.Fatal("filename")
	}
}
func TestComputerExportChunkWriterRejectsOverflowWithoutPartialWrite(t *testing.T) {
	w := computerAssetExportChunkWriter{total: ComputerAssetExportMaxBytes, digest: sha256.New()}
	if n, err := w.Write([]byte{1}); n != 0 || err == nil || w.total != ComputerAssetExportMaxBytes || len(w.buffer) != 0 {
		t.Fatal("overflow wrote data", n, err)
	}
}

func TestComputerExportTimeRangeMatchesListAndExcludesMissingFacts(t *testing.T) {
	o := computerObservation()
	defer o.Discard()
	for _, tc := range []struct {
		start, end string
		count      int
	}{
		{"2026-01-01T00:00:00Z", "2026-01-02T00:00:00Z", 21},
		{"2025-12-31T00:00:00Z", "2026-01-01T00:00:00Z", 0},
		{"0001-01-01T00:00:00Z", "0001-01-02T00:00:00Z", 1},
		{"2026-01-01T01:00:00+01:00", "2026-01-02T01:00:00+01:00", 21},
	} {
		for _, order := range []string{"", "asc", "desc"} {
			p := ComputerAssetExportV3Payload{DomainID: "domain", ExpectedRevision: "1", ExpectedCredentialRevision: "2", ObservationID: "obs", StartTm: tc.start, EndTm: tc.end, CreatTm: order, ScreenColumns: []string{"dn", "guid"}}
			raw, _ := json.Marshal(p)
			canonical, err := ValidateComputerAssetExportV3Payload(raw)
			if err != nil {
				t.Fatal(err)
			}
			var decoded ComputerAssetExportV3Payload
			if json.Unmarshal(canonical, &decoded) != nil || !reflect.DeepEqual(decoded, p) {
				t.Fatal("dates or columns changed")
			}
			rows, count, err := computerAssetsV3Rows(context.Background(), o, p.Filter())
			if err != nil || count != 23 || len(rows) != tc.count {
				t.Fatal(tc, count, len(rows), err)
			}
			var all []directoryassets.PublicObjectV3
			for page := 1; ; page++ {
				f := p.Filter()
				f.PageIdx = page
				got, err := computerAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, got.List...)
				if page >= got.Page.Pages {
					break
				}
			}
			if len(all) != len(rows) {
				t.Fatal("filtered total differs")
			}
			for i := range rows {
				if !reflect.DeepEqual(all[i], rows[i]) {
					t.Fatal("filtered facts/order differ")
				}
			}
		}
	}
	for _, suffix := range []string{`,"startTm":"2026-01-01T00:00:00Z"`, `,"endTm":"2026-01-02T00:00:00Z"`, `,"startTm":"2026-01-01","endTm":"2026-01-02"`, `,"startTm":"2026-01-02T00:00:00Z","endTm":"2026-01-01T00:00:00Z"`, `,"startTm":"2026-01-01T00:00:00Z","endTm":"2026-01-01T00:00:00Z"`} {
		if _, err := ValidateComputerAssetExportV3Payload([]byte(strings.TrimSuffix(validComputerExportPayload(), "}") + suffix + "}")); err == nil {
			t.Fatal("invalid range accepted", suffix)
		}
	}
	for _, search := range []string{strings.Repeat("😀", 25), strings.Repeat("a", 50)} {
		raw := strings.Replace(validComputerExportPayload(), "}", `,"search":"`+search+`"}`, 1)
		if _, err := ValidateComputerAssetExportV3Payload([]byte(raw)); err != nil {
			t.Fatal("valid boundary rejected", err)
		}
	}
}
