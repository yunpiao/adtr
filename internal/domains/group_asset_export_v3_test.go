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

func validGroupExportPayload() string {
	return `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"2","observationId":"observed","screenColumns":[]}`
}
func TestGroupAssetExportV3CanonicalColumnsAndClosedPayload(t *testing.T) {
	raw, err := ValidateGroupAssetExportV3Payload([]byte(validGroupExportPayload()))
	if err != nil {
		t.Fatal(err)
	}
	var p GroupAssetExportV3Payload
	if json.Unmarshal(raw, &p) != nil || !reflect.DeepEqual(p.ScreenColumns, groupAssetExportColumns) || p.Search != "" {
		t.Fatal("defaults", string(raw))
	}
	for _, input := range []string{`{}`, `null`, strings.Replace(validGroupExportPayload(), `[]`, `null`, 1), strings.Replace(validGroupExportPayload(), `[]`, `["guid","guid"]`, 1), strings.Replace(validGroupExportPayload(), `[]`, `["status"]`, 1), strings.Replace(validGroupExportPayload(), `"observed"`, `""`, 1), strings.Replace(validGroupExportPayload(), `"1"`, `1`, 1), strings.Replace(validGroupExportPayload(), `}`, `,"pageSize":10}`, 1), strings.Replace(validGroupExportPayload(), `}`, `,"domainId":"two"}`, 1), strings.Replace(validGroupExportPayload(), `}`, `,"search":"\ud800"}`, 1), strings.Replace(validGroupExportPayload(), `}`, `,"search":"\udc00"}`, 1), validGroupExportPayload() + `{}`} {
		if _, err := ValidateGroupAssetExportV3Payload([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, search := range []string{`\ud83d\ude00`, `\\ud800`, `\u0000`} {
		input := strings.Replace(validGroupExportPayload(), `}`, `,"search":"`+search+`"}`, 1)
		if _, err := ValidateGroupAssetExportV3Payload([]byte(input)); err != nil {
			t.Fatal("valid lossless search", search, err)
		}
	}
}
func TestGroupAssetExportKindIsIsolatedAndGated(t *testing.T) {
	k := New(nil).GroupAssetExportV3Kind()
	if k.Name != GroupAssetExportV3KindName || k.Platform || !k.OwnerScoped || !k.ReplaySafe || !k.CancelDiscardsResult || k.Schedulable || k.MaxAttempts != 3 || k.ValidateSuccess == nil {
		t.Fatal("unsafe policy")
	}
	if _, err := tasks.NewRegistry(k); err != nil {
		t.Fatal(err)
	}
	if out := k.Execute(context.Background(), tasks.Execution{Task: tasks.Task{Payload: []byte(validGroupExportPayload())}}); out.Code != "fencing_required" || out.State != tasks.Failed {
		t.Fatal("unguarded execution")
	}
}
func TestGroupAssetArtifactResultRejectsMalformedMetadata(t *testing.T) {
	base := `{"artifactToken":1,"rowCount":0,"byteCount":100,"sha256":"` + strings.Repeat("a", 64) + `"}`
	if _, err := decodeGroupAssetExportResult([]byte(base)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(base, `"rowCount":0,`, "", 1), strings.Replace(base, `"artifactToken":1`, `"artifactToken":0`, 1), strings.Replace(base, `"rowCount":0`, `"rowCount":10001`, 1), strings.Replace(base, `"byteCount":100`, `"byteCount":33554433`, 1), strings.Replace(base, strings.Repeat("a", 64), strings.Repeat("A", 64), 1), strings.Replace(base, `}`, `,"extra":1}`, 1), strings.Replace(base, `}`, `,"rowCount":0}`, 1)} {
		if _, err := decodeGroupAssetExportResult([]byte(bad)); err == nil {
			t.Fatal("malformed metadata accepted")
		}
	}
}

func TestGroupExportPredicateAndOrderExactlyMatchEveryListPage(t *testing.T) {
	o := groupObservation()
	defer o.Discard()
	for _, search := range []string{"", "group-", "group-02", " group", "mail-only", "S-1-5", "CN="} {
		for _, order := range []string{"", "asc", "desc"} {
			f := groupFilter()
			f.Search, f.CreatTm, f.ObservationID = search, order, "obs"
			rows, count, err := groupAssetsV3Rows(context.Background(), o, f)
			if err != nil || count != 23 {
				t.Fatal(count, err)
			}
			var all []string
			for page := 1; ; page++ {
				f.PageIdx = page
				got, err := groupAssetsV3Page(context.Background(), UserAssetsV2Selection{}, "obs", o, f)
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
func TestGroupExportRejectsUserFiltersAndPreservesRawSearchAndColumns(t *testing.T) {
	for _, field := range []string{"sAMAccountName", "startTm", "endTm", "mail", "pageIdx", "pageSize", "overviewType", "Search", "DomainID", "ScreenColumns"} {
		raw := strings.Replace(validGroupExportPayload(), "}", `,"`+field+`":"x"}`, 1)
		if _, err := ValidateGroupAssetExportV3Payload([]byte(raw)); err == nil {
			t.Fatal("accepted user/unsupported filter", field)
		}
	}
	for _, search := range []string{strings.Repeat("😀", 11), strings.Repeat("a", 21)} {
		raw := strings.Replace(validGroupExportPayload(), "}", `,"search":"`+search+`"}`, 1)
		if _, err := ValidateGroupAssetExportV3Payload([]byte(raw)); err == nil {
			t.Fatal("accepted oversized search")
		}
	}
	raw := strings.Replace(validGroupExportPayload(), `[]`, `["dn","guid"]`, 1)
	raw = strings.Replace(raw, "}", `,"search":"  x ","creatTm":"desc"}`, 1)
	canonical, err := ValidateGroupAssetExportV3Payload([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var p GroupAssetExportV3Payload
	if json.Unmarshal(canonical, &p) != nil || p.Search != "  x " || !reflect.DeepEqual(p.ScreenColumns, []string{"dn", "guid"}) {
		t.Fatal("raw search/columns changed")
	}
}

func TestGroupExportProjectionPreservesSelectedOrderAndMissingScalars(t *testing.T) {
	row := directoryassets.PublicObjectV3{PublicObjectV2: directoryassets.PublicObjectV2{Object: directoryassets.Object{GUID: "id", DN: "CN=x"}}}
	if got := groupAssetExportHeaders([]string{"dn", "guid", "sAMAccountName", "sid", "whenCreated"}); !reflect.DeepEqual(got, []string{"DN", "GUID", "SAM", "SID", "whenCreated"}) {
		t.Fatal(got)
	}
	if got := groupAssetExportCells(row, []string{"dn", "guid", "sAMAccountName", "sid", "whenCreated"}); !reflect.DeepEqual(got, []string{"CN=x", "id", "", "", ""}) {
		t.Fatal(got)
	}
	if GroupAssetExportV3Filename("task") != "group-assets-v3-task.xlsx" {
		t.Fatal("filename")
	}
}
func TestGroupExportChunkWriterRejectsOverflowWithoutPartialWrite(t *testing.T) {
	w := groupAssetExportChunkWriter{total: GroupAssetExportMaxBytes, digest: sha256.New()}
	if n, err := w.Write([]byte{1}); n != 0 || err == nil || w.total != GroupAssetExportMaxBytes || len(w.buffer) != 0 {
		t.Fatal("overflow wrote data", n, err)
	}
}
