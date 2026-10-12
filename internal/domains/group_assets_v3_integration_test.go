//go:build integration

package domains_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
	"testing"
)

// Real PG/engine/publication/authorization contracts with a labeled synthetic
// executor. This does not substitute for the separate registered TLS E2E chain.
func groupRuntimeObservation() ldapconnection.DirectoryV3Observation {
	o := directoryV3RuntimeObservation()
	base := o.Objects[0]
	o.Objects = nil
	for i := 23; i >= 1; i-- {
		v := base
		v.Base.Kind = directoryassets.Group
		v.Base.Classes = []string{"group", "top"}
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		sam := fmt.Sprintf("group-%02d", i)
		v.Base.SAMAccountName = &sam
		o.Objects = append(o.Objects, v)
	}
	base.Base.GUID = "ffffffff-4455-6677-8899-aabbccddeeff"
	o.Objects = append(o.Objects, base)
	base.Base.GUID = "ffffffff-4455-6677-8899-aabbccddee00"
	base.Base.Kind = directoryassets.Computer
	base.Base.Classes = []string{"computer", "top", "user"}
	o.Objects = append(o.Objects, base)
	return o
}
func publishGroupRuntime(f *directoryUseFixture, o ldapconnection.DirectoryV3Observation, key string) tasks.Task {
	f.t.Helper()
	kind := f.store.DirectoryV3Kind()
	kind.Execute = domains.PublishDirectoryV3ObservationForTest(f.store, o)
	installDirectoryV3RuntimeKind(f.t, f, kind)
	task := submitDirectoryV3Runtime(f, key)
	if _, e := f.engine.RunOne(context.Background(), "group-test-owner"); e != nil {
		f.t.Fatal(e)
	}
	return task
}
func readGroupRuntime(f *directoryUseFixture, s *domains.Store, in domains.GroupAssetsV3Filter, allowed []string) (out domains.GroupAssetsV3List, err error) {
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		out, err = s.GroupAssetsV3ListTx(ctx, tx, f.p.TenantID, allowed, in)
		return err
	})
	return
}
func groupRuntimeFilter(f *directoryUseFixture, pin string) domains.GroupAssetsV3Filter {
	return domains.GroupAssetsV3Filter{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: pin, PageIdx: 1, PageSize: 10}
}
func TestGroupAssetsV3StoredPinsCountsAndSourceAuthority(t *testing.T) {
	f := directoryV3RuntimeFixture(t)
	grantDirectoryV3(f)
	in := groupRuntimeFilter(f, "")
	absent, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || absent.Available || absent.ObservedGroupCount != nil || absent.Page.Total != 0 {
		t.Fatal(absent, e)
	}
	o := groupRuntimeObservation()
	defer o.Discard()
	task := publishGroupRuntime(f, o, "group-first")
	in.ObservationID = task.ID
	first, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || !first.Available || *first.ObservedGroupCount != 23 || first.Page.Total != 23 || len(first.List) != 10 {
		t.Fatal(first, e)
	}
	in.PageIdx = 2
	second, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || len(second.List) != 10 || second.List[0].GUID <= first.List[9].GUID {
		t.Fatal(second, e)
	}
	in.PageIdx = 1
	in.Search = "group-23"
	filtered, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || filtered.Page.Total != 1 || *filtered.ObservedGroupCount != 23 {
		t.Fatal(filtered, e)
	}
	in.Search = "not present"
	zero, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || !zero.Available || zero.Page.Total != 0 || *zero.ObservedGroupCount != 23 {
		t.Fatal(zero, e)
	}
	in.Search = ""
	empty := directoryV3RuntimeObservation()
	defer empty.Discard()
	latest := publishGroupRuntime(f, empty, "group-no-groups")
	pinned, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || pinned.ObservationID != task.ID || *pinned.ObservedGroupCount != 23 {
		t.Fatal("pin fell forward", e)
	}
	in.ObservationID = ""
	observedZero, e := readGroupRuntime(f, f.store, in, []string{f.id})
	if e != nil || !observedZero.Available || observedZero.ObservationID != latest.ID || *observedZero.ObservedGroupCount != 0 {
		t.Fatal(observedZero, e)
	}
	disabled := domains.New(directoryV3RuntimeWithGates(f, false, false))
	if got, e := readGroupRuntime(f, disabled, in, []string{f.id}); e != nil || !got.Available {
		t.Fatal("producer flags blocked stored reader", e)
	}
	for _, tc := range []struct {
		pin, revision string
		scope         []string
		code          string
	}{{task.ID, "999", nil, "not_found"}, {task.ID, "999", []string{f.id}, "selection_changed"}, {"missing", "2", []string{f.id}, "directory_observation_unavailable"}} {
		q := in
		q.ObservationID, q.ExpectedRevision = tc.pin, tc.revision
		out, e := readGroupRuntime(f, f.store, q, tc.scope)
		var p *domains.Error
		if !errors.As(e, &p) || p.Code != tc.code || out.Available {
			t.Fatal(tc.code, e)
		}
	}
}
func TestGroupAssetsV3ExcludedAndOffPageCorruptionInvalidatesFullRead(t *testing.T) {
	for _, guid := range []string{"ffffffff-4455-6677-8899-aabbccddeeff", "ffffffff-4455-6677-8899-aabbccddee00", "00000017-4455-6677-8899-aabbccddeeff"} {
		t.Run(guid, func(t *testing.T) {
			f := directoryV3RuntimeFixture(t)
			grantDirectoryV3(f)
			o := groupRuntimeObservation()
			defer o.Discard()
			task := publishGroupRuntime(f, o, "group-hidden-corruption")
			var body []byte
			if e := f.conn.QueryRow(context.Background(), `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&body); e != nil {
				t.Fatal(e)
			}
			// Corrupt an excluded user/computer or off-page group row, recalculate digest, and preserve count.
			marker := []byte(`"objectGUID":"` + guid + `"`)
			at := bytes.Index(body, marker)
			if at < 0 {
				t.Fatal("missing selected corruption target")
			}
			body = append(append([]byte{}, body[:at]...), bytes.Replace(body[at:], []byte(`"primaryGroupIDBytes":"NTEz"`), []byte(`"primaryGroupIDBytes":"@@@"`), 1)...)
			sum := sha256.Sum256(body)
			f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
			f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3 WHERE task_id=$1`, task.ID, body, hex.EncodeToString(sum[:]))
			f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
			clear(body)
			q := groupRuntimeFilter(f, task.ID)
			q.Search = "no match"
			out, e := readGroupRuntime(f, f.store, q, []string{f.id})
			var p *domains.Error
			if !errors.As(e, &p) || p.Code != "directory_observation_unavailable" || out.Available || out.List != nil {
				t.Fatal("hidden corrupt row bypassed full validation", e)
			}

		})
	}
}
