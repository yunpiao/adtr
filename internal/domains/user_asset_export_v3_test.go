package domains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
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

func TestUserAssetExportV3UACOmissionPreservesCanonicalBytesAndHash(t *testing.T) {
	// Exact pre-change field order/defaults: do not replace this with marshaling
	// the new struct, which would conceal a backwards-incompatible encoding.
	const old = `{"domainId":"one","expectedRevision":"1","expectedCredentialRevision":"2","observationId":"observed","search":"","sAMAccountName":"","startTm":"","endTm":"","creatTm":"","screenColumns":["guid","sAMAccountName","sid","mail","dn","whenCreated"]}`
	raw, err := ValidateUserAssetExportV3Payload([]byte(validUserExportPayload()))
	if err != nil || string(raw) != old || sha256.Sum256(raw) != sha256.Sum256([]byte(old)) {
		t.Fatal("old payload bytes/hash changed", err, string(raw))
	}
	// The existing engine sorts object keys and prefixes version/parent before
	// hashing. Pin the historical engine hash too, without adding a new codec.
	var taskPayload map[string]any
	if json.Unmarshal(raw, &taskPayload) != nil {
		t.Fatal("canonical task payload")
	}
	sorted, err := json.Marshal(taskPayload)
	if err != nil {
		t.Fatal(err)
	}
	taskHash := sha256.Sum256(append([]byte("1::"), sorted...))
	if fmt.Sprintf("%x", taskHash) != "4b8cedc2869648ea1538f46219bb6de6a239e243236fbb1196a2ffb35b6708d6" {
		t.Fatal("historical task hash changed")
	}
	oldAgain, err := ValidateUserAssetExportV3Payload([]byte(old))
	if err != nil || !bytes.Equal(raw, oldAgain) {
		t.Fatal("old canonical receipt no longer readable", err)
	}
	hashes := map[[32]byte]bool{sha256.Sum256(raw): true}
	for _, filter := range []string{"set", "clear", "not_returned"} {
		input := strings.TrimSuffix(validUserExportPayload(), "}") + `,"uacDisableBit":"` + filter + `"}`
		canonical, err := ValidateUserAssetExportV3Payload([]byte(input))
		if err != nil {
			t.Fatal(filter, err)
		}
		var p UserAssetExportV3Payload
		if json.Unmarshal(canonical, &p) != nil || p.UACDisableBit == nil || *p.UACDisableBit != filter || p.Filter().UACDisableBit != filter {
			t.Fatal("filter lost from canonical request")
		}
		hash := sha256.Sum256(canonical)
		if hashes[hash] {
			t.Fatal("distinct filters share payload hash")
		}
		hashes[hash] = true
		again, err := ValidateUserAssetExportV3Payload(canonical)
		if err != nil || !bytes.Equal(canonical, again) {
			t.Fatal("new canonical request is not stable", err)
		}
	}
}

func TestUserAssetExportV3UACExplicitInvalidValuesNeverBecomeOmitted(t *testing.T) {
	for _, value := range []string{`""`, `null`, `0`, `2`, `true`, `[]`, `{}`, `["set"]`, `"SET"`, `" clear"`, `"not-returned"`, `"disabled"`} {
		t.Run(value, func(t *testing.T) {
			raw := strings.TrimSuffix(validUserExportPayload(), "}") + `,"uacDisableBit":` + value + `}`
			_, err := ValidateUserAssetExportV3Payload([]byte(raw))
			requireUserAssetsV2Error(t, err, 400, "invalid_input")
		})
	}
	for _, suffix := range []string{`,"uacDisableBit":"set","uacDisableBit":"set"}`, `,"uacDisableBit":"set","uacDisableBit":"clear"}`, `,"uacDisableBit":"set","unknown":"x"}`} {
		_, err := ValidateUserAssetExportV3Payload([]byte(strings.TrimSuffix(validUserExportPayload(), "}") + suffix))
		requireUserAssetsV2Error(t, err, 400, "invalid_input")
	}
	empty := ""
	p := UserAssetExportV3Payload{UACDisableBit: &empty}
	encoded, err := json.Marshal(p)
	if err != nil || !bytes.Contains(encoded, []byte(`"uacDisableBit":""`)) {
		t.Fatal(fmt.Sprintf("present-empty erased: %s", encoded), err)
	}
}
