package domains

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestDirectoryV2KindKeepsExactIndependentIdentity(t *testing.T) {
	s := New(nil)
	kind, legacy := s.DirectoryV2Kind(), s.DirectoryKind()
	if kind.Name != DirectoryV2KindName || kind.Version != 1 || kind.MaxAttempts != 1 || !kind.SingleAttemptOnly || kind.ReplaySafe || kind.Schedulable || kind.Platform || kind.OwnerScoped || !kind.CancelDiscardsResult || len(kind.RetryCodes) != 0 || kind.OnQuiesced == nil || kind.Timeout != 125*time.Second || kind.Lease != 30*time.Second || kind.Heartbeat != time.Second {
		t.Fatal("v2 widened the one-attempt lifecycle")
	}
	if _, err := tasks.NewRegistry(legacy, kind); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []json.RawMessage{validAccountPins().json(), validDirectoryPins().json(), nil} {
		if _, err := kind.Validate(raw); err == nil {
			t.Fatal("v2 accepted another profile")
		}
	}
	if _, err := legacy.Validate(directoryV2UseTestTask().Payload); err == nil {
		t.Fatal("v1 accepted the v2 pin")
	}
}

func TestDirectoryV2AuthorityAndExecutorRejectProfileBeforeSQL(t *testing.T) {
	s := New(nil)
	pins := directoryUseTestPins()
	for _, task := range []tasks.Task{directoryUseTestTask(), {}, {Kind: DirectoryV2KindName, PayloadVersion: 1, ActorID: 1, Payload: pins.json()}} {
		if _, err := s.currentDirectoryForProfile(context.Background(), nil, directoryTaskV2, task, pins); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("wrong profile reached runtime or SQL", err)
		}
		out := s.DirectoryV2Kind().Execute(context.Background(), tasks.Execution{Task: task, WithTx: func(context.Context, int64, tasks.FencedWork) (int64, error) {
			t.Fatal("wrong profile reached a fenced transaction")
			return 0, nil
		}})
		assertDirectoryOutcome(t, out, tasks.Failed, "invalid_input")
	}
	// The existing master gate alone never enables the v2 reader.
	legacy, _, _ := directoryExecutorStubFixture(t)
	task := directoryV2UseTestTask()
	pins.PolicyRevision = legacy.policyRevision()
	task.Payload = directoryTaskV2.payload(pins)
	_, err := legacy.currentDirectoryForProfile(context.Background(), nil, directoryTaskV2, task, pins)
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "directory_read_disabled" {
		t.Fatal("master gate implied v2 access", err)
	}
	for _, profile := range []directoryTaskProfile{0, 3} {
		if _, _, err := directoryGrantForProfileTx(context.Background(), nil, profile, tasks.Principal{}, row{}); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("unknown grant profile reached SQL")
		}
		if _, err := s.submitDirectoryForProfileTx(context.Background(), nil, nil, tasks.Principal{}, DirectoryInput{}, profile); !errors.Is(err, tasks.ErrAuthorization) {
			t.Fatal("unknown admission profile reached SQL")
		}
	}
}

func TestDirectoryV2ExecutorKnownCommitBeforeDecryptAndClearsFailureBuffers(t *testing.T) {
	for _, mode := range []string{"grant-denied", "open-conflict", "missing-envelope", "scan-error", "commit-error", "cancel-after-commit", "panic-in-scan", "panic-after-callback"} {
		t.Run(mode, func(t *testing.T) {
			s, task, tx := directoryExecutorStubFixtureForProfile(t, directoryTaskV2)
			tx.failure = mode
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var out tasks.Outcome
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				out = s.DirectoryV2Kind().Execute(ctx, tasks.Execution{Task: task, WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
					calls++
					if calls != 1 {
						t.Fatal("failed opening reached transport")
					}
					progress, cursor, result, err := work(ctx, tx)
					assertDirectoryCheckpoint(t, progress, cursor, result)
					if err != nil {
						return version, err
					}
					switch mode {
					case "commit-error":
						return version, errors.New("unknown synthetic commit")
					case "cancel-after-commit":
						cancel()
					case "panic-after-callback":
						panic("synthetic transaction panic")
					}
					return version + 1, nil
				}})
			}()
			if strings.HasPrefix(mode, "panic-") {
				if panicked == nil {
					t.Fatal("panic did not reach engine")
				}
			} else {
				if panicked != nil {
					t.Fatal(panicked)
				}
				state, code := tasks.Failed, "checkpoint_failed"
				switch mode {
				case "grant-denied":
					code = "authorization_revoked"
				case "open-conflict":
					code = "directory_use_changed"
				case "missing-envelope":
					code = "credential_unavailable"
				case "cancel-after-commit":
					state, code = tasks.Cancelled, "cancelled"
				}
				assertDirectoryOutcome(t, out, state, code)
			}
			if mode == "grant-denied" && (tx.openCalls != 0 || tx.envelopeCalls != 0) || mode == "open-conflict" && tx.envelopeCalls != 0 {
				t.Fatal("rejection reached credential acquisition")
			}
			if !bytes.Equal(tx.scannedEnvelope, make([]byte, len(tx.scannedEnvelope))) {
				t.Fatal("owned credential envelope retained")
			}
		})
	}
}

func TestDirectoryV2RealReaderRechecksBeforeNetworkWithStubbedFence(t *testing.T) {
	s, task, tx := directoryExecutorStubFixtureForProfile(t, directoryTaskV2)
	calls := 0
	out := s.DirectoryV2Kind().Execute(context.Background(), tasks.Execution{Task: task, WithTx: func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
		calls++
		if version != task.ResultVersion+int64(calls-1) || calls > 2 {
			t.Fatal("stale fence or continued transport")
		}
		if calls == 2 {
			tx.failure = "grant-denied"
		}
		_, _, _, err := work(ctx, tx)
		return version + 1, err
	}})
	assertDirectoryOutcome(t, out, tasks.Failed, "authorization_revoked")
	if calls != 2 || tx.openCalls != 1 || tx.envelopeCalls != 1 || !bytes.Equal(tx.scannedEnvelope, make([]byte, len(tx.scannedEnvelope))) {
		t.Fatal("reader reused opening authority or retained envelope")
	}
}

func TestDirectoryV2PageLosslessIndependentStableProjection(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	base := o.Objects[0]
	for i := 1; i < 60; i++ {
		object := base
		object.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 60-i)
		if i%2 == 0 {
			object.Base.Kind, object.Base.Classes = directoryassets.Group, []string{"group", "top"}
		}
		o.Objects = append(o.Objects, object)
	}
	out, err := directoryV2Page("snapshot", o, DirectoryFilter{Kind: directoryassets.User, PageIdx: 2, PageSize: 25})
	if err != nil || !out.Available || out.DictionaryVersion != 2 || out.Page.Total != 31 || len(out.List) != 6 || out.Page.Pages != 2 {
		t.Fatal("incorrect filtered page", out.Page, err)
	}
	for i, object := range out.List {
		if i > 0 && out.List[i-1].GUID >= object.GUID || object.Mail == nil || *object.Mail != " Case\x00😀@example.test " || object.WhenCreated == nil || *object.WhenCreated != "0001-01-01T00:00:00Z" || len(object.Description) != 1 {
			t.Fatal("page changed source text, year or stable GUID order")
		}
	}
	o.Discard()
	if *out.List[0].Mail != " Case\x00😀@example.test " {
		t.Fatal("projection aliases owned raw bytes")
	}
	empty := sampleDirectoryV2Observation()
	directoryassets.ClearStoredObjectsV2(empty.Objects)
	empty.Objects = []directoryassets.StoredObjectV2{}
	page, err := directoryV2Page("empty-snapshot", empty, DirectoryFilter{PageIdx: 1, PageSize: 25})
	if err != nil || !page.Available || page.DictionaryVersion != 2 || page.List == nil || page.Page.Total != 0 {
		t.Fatal("observed empty became unavailable", err)
	}
	for _, source := range []bool{false, true} {
		page := DirectoryV2List{DictionaryVersion: 2, Available: source, List: []directoryassets.PublicObjectV2{}, Page: Page{Index: 1, Size: 25}}
		raw, err := json.Marshal(page)
		if err != nil || !bytes.Contains(raw, []byte(`"dictionaryVersion":2`)) || !bytes.Contains(raw, []byte(`"list":[]`)) {
			t.Fatal("empty/unavailable omitted its version or empty list")
		}
	}
}

func TestDirectoryV2PageRejectsMalformedWithoutPartialRowsAndBoundsJSON(t *testing.T) {
	o := sampleDirectoryV2Observation()
	defer o.Discard()
	bad := o.Objects[0]
	bad.Base.GUID = "10112233-4455-6677-8899-aabbccddeeff"
	bad.Supplemental.WhenCreatedBytes = []byte("not-a-time")
	o.Objects = append(o.Objects, bad)
	out, err := directoryV2Page("snapshot", o, DirectoryFilter{PageIdx: 1, PageSize: 25})
	if err == nil || !reflect.DeepEqual(out, DirectoryV2List{}) {
		t.Fatal("malformed projection returned partial rows")
	}
	text := strings.Repeat("\x00", DirectoryV2PageMaxBytes/6)
	large := DirectoryV2List{DictionaryVersion: 2, List: []directoryassets.PublicObjectV2{{Mail: &text}}}
	var failure *Error
	if err := validateDirectoryV2PageSize(large); !errors.As(err, &failure) || failure.Status != 422 || failure.Code != "directory_limit_exceeded" {
		t.Fatal("escaped JSON page exceeded the safety bound", err)
	}
	_, err = New(nil).DirectoryV2ListTx(context.Background(), nil, "tenant", nil, DirectoryFilter{DomainID: "other", PageIdx: 1, PageSize: 25})
	if !errors.As(err, &failure) || failure.Status != 404 {
		t.Fatal("scope denial reached SQL", err)
	}
	if err := publishDirectoryObservationV2Tx(context.Background(), pgx.Tx(nil), directoryUseTestTask(), directoryUseTestPins(), o); !errors.Is(err, errDirectoryUseEvidence) {
		t.Fatal("v1 task reached v2 publication")
	}
}
