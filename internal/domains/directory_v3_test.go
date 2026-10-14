package domains

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
	"reflect"
	"strings"
	"testing"
	"time"
)

func directoryV3UseTestTask() tasks.Task {
	task := directoryUseTestTask()
	task.Kind = DirectoryV3KindName
	task.Payload = directoryTaskV3.payload(directoryUseTestPins())
	return task
}
func sampleDirectoryV3Observation() ldapconnection.DirectoryV3Observation {
	old := sampleDirectoryV2Observation()
	objects := make([]directoryassets.StoredObjectV3, len(old.Objects))
	for i, o := range old.Objects {
		objects[i] = directoryassets.StoredObjectV3{Base: o.Base, Supplemental: o.Supplemental, Identity: directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("0"), ServicePrincipalNameBytes: [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀")}}}
	}
	return ldapconnection.DirectoryV3Observation{Objects: objects, Source: old.Source}
}
func TestDirectoryV3ExactLifecyclePurposeAndPayload(t *testing.T) {
	s := New(nil)
	kind := s.DirectoryV3Kind()
	if kind.Name != credentialuse.DirectoryV3Purpose || kind.Version != 1 || kind.MaxAttempts != 1 || !kind.SingleAttemptOnly || kind.ReplaySafe || kind.Schedulable || kind.Platform || kind.OwnerScoped || !kind.CancelDiscardsResult || len(kind.RetryCodes) != 0 || kind.OnQuiesced == nil || kind.Timeout != 125*time.Second {
		t.Fatal("v3 policy widened")
	}
	tuple, ok := directoryTaskV3.identity()
	if !ok || tuple.purpose != credentialuse.DirectoryV3Purpose || tuple.dictionaryVersion != 3 {
		t.Fatal("v3 identity mismatch")
	}
	task := directoryV3UseTestTask()
	if raw, err := kind.Validate(task.Payload); err != nil || !bytes.Equal(raw, task.Payload) {
		t.Fatal("v3 payload", err)
	}
	for _, other := range []directoryTaskProfile{directoryTaskV1, directoryTaskV2, 0, 4} {
		if _, err := validateDirectoryPayloadForProfile(other, task.Payload); err == nil {
			t.Fatal("cross-profile payload accepted")
		}
		if _, err := directoryUseTaskPayloadForProfile(other, task); err == nil {
			t.Fatal("cross-profile use accepted")
		}
	}
	for _, raw := range []json.RawMessage{directoryUseTestTask().Payload, directoryV2UseTestTask().Payload, bytes.Replace(task.Payload, []byte(`"dictionaryVersion":3`), []byte(`"dictionaryVersion":null`), 1), bytes.Replace(task.Payload, []byte(`"dictionaryVersion":3`), []byte(`"dictionaryVersion":3,"dictionaryVersion":3`), 1)} {
		if _, err := kind.Validate(raw); err == nil {
			t.Fatal("ambiguous v3 payload accepted")
		}
	}
	for _, old := range []tasks.Task{directoryUseTestTask(), directoryV2UseTestTask(), {}} {
		if _, err := s.currentDirectoryForProfile(context.Background(), nil, directoryTaskV3, old, directoryUseTestPins()); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("cross-profile reached SQL")
		}
	}
}
func TestDirectoryV3ObservationCanonicalBoundsAndIsolation(t *testing.T) {
	o := sampleDirectoryV3Observation()
	defer o.Discard()
	raw, err := encodeDirectoryObservationV3(o)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	got, err := decodeDirectoryObservationV3(raw, digest, len(o.Objects))
	if err != nil || !reflect.DeepEqual(got, o) {
		t.Fatal("roundtrip", err)
	}
	defer got.Discard()
	old := sampleDirectoryV2Observation()
	defer old.Discard()
	oldRaw, err := encodeDirectoryObservationV2(old)
	if err != nil {
		t.Fatal(err)
	}
	oldSum := sha256.Sum256(oldRaw)
	if _, err = decodeDirectoryObservationV3(oldRaw, hex.EncodeToString(oldSum[:]), len(old.Objects)); err == nil {
		t.Fatal("v2 backfilled")
	}
	if _, err = decodeDirectoryObservationV2(raw, digest, len(o.Objects)); err == nil {
		t.Fatal("v3 read as v2")
	}
	for _, body := range [][]byte{append([]byte(" "), raw...), bytes.Replace(raw, []byte(`"dictionaryVersion":3`), []byte(`"dictionaryVersion":3,"dictionaryVersion":3`), 1), bytes.Replace(raw, []byte(`"primaryGroupIDBytes"`), []byte(`"unknown"`), 1), bytes.Repeat([]byte{' '}, directoryObservationMaxBytes+1)} {
		sum := sha256.Sum256(body)
		out, err := decodeDirectoryObservationV3(body, hex.EncodeToString(sum[:]), len(o.Objects))
		if err == nil || !reflect.DeepEqual(out, ldapconnection.DirectoryV3Observation{}) {
			t.Fatal("corruption retained output")
		}
	}
}
func TestDirectoryV3PublicPageIdentityAndFullEnvelopeBudget(t *testing.T) {
	o := sampleDirectoryV3Observation()
	defer o.Discard()
	out, err := directoryV3Page("observed", o, DirectoryFilter{PageIdx: 1, PageSize: 25})
	if err != nil || out.DictionaryVersion != 3 || !out.Available || len(out.List) != 1 {
		t.Fatal("page", err)
	}
	if out.List[0].PrimaryGroupID == nil || *out.List[0].PrimaryGroupID != 0 || !reflect.DeepEqual(out.List[0].ServicePrincipalName, []string{"HOST/Z", "host/a\x00\n😀"}) {
		t.Fatal("public facts changed")
	}
	if err := validateDirectoryV3PageSize(DirectoryV3List{DictionaryVersion: 3, ObservationID: strings.Repeat("x", DirectoryV3PageMaxBytes)}); err == nil {
		t.Fatal("envelope not included in response bound")
	}
	bad := o.Objects[0]
	bad.Identity.ServicePrincipalNameBytes = [][]byte{{0xff}}
	o.Objects = append(o.Objects, bad)
	if page, err := directoryV3Page("observed", o, DirectoryFilter{PageIdx: 1, PageSize: 25}); err == nil || !reflect.DeepEqual(page, DirectoryV3List{}) {
		t.Fatal("malformed page partially exposed")
	}
}

func TestDirectoryV3ObservedCountsPrecedeKindAndPagination(t *testing.T) {
	o := sampleDirectoryV3Observation()
	defer o.Discard()
	base := o.Objects[0]
	o.Objects = nil
	for i, kind := range []directoryassets.Kind{directoryassets.User, directoryassets.User, directoryassets.Group, directoryassets.Computer} {
		object := base
		object.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i+1)
		object.Base.Kind = kind
		object.Base.Classes = []string{"top", "user"}
		if kind == directoryassets.Group {
			object.Base.Classes = []string{"group", "top"}
		}
		if kind == directoryassets.Computer {
			object.Base.Classes = []string{"computer", "top", "user"}
		}
		o.Objects = append(o.Objects, object)
	}
	// Exercise the same canonical full-body validator used by stored reads.
	raw, err := encodeDirectoryObservationV3(o)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	sum := sha256.Sum256(raw)
	validated, err := decodeDirectoryObservationV3(raw, hex.EncodeToString(sum[:]), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer validated.Discard()
	want := DirectoryV3ObservedCounts{User: 2, Group: 1, Computer: 1}
	for _, kind := range []directoryassets.Kind{"", directoryassets.User, directoryassets.Group, directoryassets.Computer} {
		for _, page := range []int{1, 2, 100} {
			out, err := directoryV3Page("observed", validated, DirectoryFilter{Kind: kind, PageIdx: page, PageSize: 1})
			if err != nil || out.ObservedCounts == nil || *out.ObservedCounts != want {
				t.Fatal("counts changed with filter/page", kind, page, out.ObservedCounts, err)
			}
			if out.ObservationID != "observed" || !reflect.DeepEqual(*out.Source, o.Source) {
				t.Fatal("count source changed")
			}
			total := map[directoryassets.Kind]int{"": 4, directoryassets.User: 2, directoryassets.Group: 1, directoryassets.Computer: 1}[kind]
			if out.Page.Total != total {
				t.Fatal("filtered total changed")
			}
		}
	}
}

func TestDirectoryV3ObservedCountsEmptyAndUnavailableJSON(t *testing.T) {
	o := sampleDirectoryV3Observation()
	defer o.Discard()
	directoryassets.ClearStoredObjectsV3(o.Objects)
	o.Objects = nil
	empty, err := directoryV3Page("empty", o, DirectoryFilter{PageIdx: 1, PageSize: 25})
	if err != nil || empty.ObservedCounts == nil || *empty.ObservedCounts != (DirectoryV3ObservedCounts{}) {
		t.Fatal("empty observation lacks explicit zero counts", err)
	}
	for _, tc := range []struct {
		value DirectoryV3List
		want  string
	}{{DirectoryV3List{}, `"observedCounts":null`}, {empty, `"observedCounts":{"user":0,"group":0,"computer":0}`}} {
		raw, err := json.Marshal(tc.value)
		if err != nil || !bytes.Contains(raw, []byte(tc.want)) {
			t.Fatal("required count shape missing", string(raw), err)
		}
	}
}
