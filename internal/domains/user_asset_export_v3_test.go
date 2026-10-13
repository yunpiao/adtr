package domains

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yunpiao/adtr/internal/tasks"
)

func validUserExportPayload() string {
	return `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"2","observationId":"observed","screenColumns":[]}`
}
func TestUserAssetExportV3CanonicalColumnsAndClosedPayload(t *testing.T) {
	raw, err := ValidateUserAssetExportV3Payload([]byte(validUserExportPayload()))
	if err != nil {
		t.Fatal(err)
	}
	var p UserAssetExportV3Payload
	if json.Unmarshal(raw, &p) != nil || !reflect.DeepEqual(p.ScreenColumns, userAssetExportColumns) || p.Search != "" {
		t.Fatal("defaults", string(raw))
	}
	for _, input := range []string{`{}`, `null`, strings.Replace(validUserExportPayload(), `[]`, `null`, 1), strings.Replace(validUserExportPayload(), `[]`, `["guid","guid"]`, 1), strings.Replace(validUserExportPayload(), `[]`, `["status"]`, 1), strings.Replace(validUserExportPayload(), `"observed"`, `""`, 1), strings.Replace(validUserExportPayload(), `"1"`, `1`, 1), strings.Replace(validUserExportPayload(), `}`, `,"pageSize":10}`, 1), strings.Replace(validUserExportPayload(), `}`, `,"domainId":"two"}`, 1), strings.Replace(validUserExportPayload(), `}`, `,"search":"\ud800"}`, 1), strings.Replace(validUserExportPayload(), `}`, `,"search":"\udc00"}`, 1), validUserExportPayload() + `{}`} {
		if _, err := ValidateUserAssetExportV3Payload([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	for _, search := range []string{`\ud83d\ude00`, `\\ud800`, `\u0000`} {
		input := strings.Replace(validUserExportPayload(), `}`, `,"search":"`+search+`"}`, 1)
		if _, err := ValidateUserAssetExportV3Payload([]byte(input)); err != nil {
			t.Fatal("valid lossless search", search, err)
		}
	}
}
func TestUserAssetExportKindIsIsolatedAndGated(t *testing.T) {
	k := New(nil).UserAssetExportV3Kind()
	if k.Name != UserAssetExportV3KindName || k.Platform || !k.OwnerScoped || !k.ReplaySafe || !k.CancelDiscardsResult || k.Schedulable || k.MaxAttempts != 3 || k.ValidateSuccess == nil {
		t.Fatal("unsafe policy")
	}
	if _, err := tasks.NewRegistry(k); err != nil {
		t.Fatal(err)
	}
	if out := k.Execute(context.Background(), tasks.Execution{Task: tasks.Task{Payload: []byte(validUserExportPayload())}}); out.Code != "fencing_required" || out.State != tasks.Failed {
		t.Fatal("unguarded execution")
	}
}
func TestUserAssetArtifactResultRejectsMalformedMetadata(t *testing.T) {
	base := `{"artifactToken":1,"rowCount":0,"byteCount":100,"sha256":"` + strings.Repeat("a", 64) + `"}`
	if _, err := decodeUserAssetExportResult([]byte(base)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(base, `"rowCount":0,`, "", 1), strings.Replace(base, `"artifactToken":1`, `"artifactToken":0`, 1), strings.Replace(base, `"rowCount":0`, `"rowCount":10001`, 1), strings.Replace(base, `"byteCount":100`, `"byteCount":33554433`, 1), strings.Replace(base, strings.Repeat("a", 64), strings.Repeat("A", 64), 1), strings.Replace(base, `}`, `,"extra":1}`, 1), strings.Replace(base, `}`, `,"rowCount":0}`, 1)} {
		if _, err := decodeUserAssetExportResult([]byte(bad)); err == nil {
			t.Fatal("malformed metadata accepted")
		}
	}
}
