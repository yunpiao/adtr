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
func computerRuntimeObservation() ldapconnection.DirectoryV3Observation {
	o := directoryV3RuntimeObservation()
	base := o.Objects[0]
	o.Objects = nil
	for i := 23; i >= 1; i-- {
		v := base
		v.Base.Kind = directoryassets.Computer
		v.Base.Classes = []string{"computer", "top", "user"}
		v.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i)
		sam := fmt.Sprintf("computer-%02d$", i)
		v.Base.SAMAccountName = &sam
		o.Objects = append(o.Objects, v)
	}
	base.Base.GUID = "ffffffff-4455-6677-8899-aabbccddeeff"
	o.Objects = append(o.Objects, base)
	return o
}
func publishComputerRuntime(f *directoryUseFixture, o ldapconnection.DirectoryV3Observation, key string) tasks.Task {
	f.t.Helper()
	kind := f.store.DirectoryV3Kind()
	kind.Execute = domains.PublishDirectoryV3ObservationForTest(f.store, o)
	installDirectoryV3RuntimeKind(f.t, f, kind)
	task := submitDirectoryV3Runtime(f, key)
	if _, e := f.engine.RunOne(context.Background(), "computer-test-owner"); e != nil {
		f.t.Fatal(e)
	}
	return task
}
func readComputerRuntime(f *directoryUseFixture, s *domains.Store, in domains.ComputerAssetsV3Filter, allowed []string) (out domains.ComputerAssetsV3List, err error) {
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		out, err = s.ComputerAssetsV3ListTx(ctx, tx, f.p.TenantID, allowed, in)
		return err
	})
	return
}
func computerRuntimeFilter(f *directoryUseFixture, pin string) domains.ComputerAssetsV3Filter {
	return domains.ComputerAssetsV3Filter{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: pin, PageIdx: 1, PageSize: 10}
}
func TestComputerAssetsV3StoredPinsCountsAndSourceAuthority(t *testing.T) {
	f := directoryV3RuntimeFixture(t)
	grantDirectoryV3(f)
	in := computerRuntimeFilter(f, "")
	absent, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || absent.Available || absent.ObservedComputerCount != nil || absent.Page.Total != 0 {
		t.Fatal(absent, e)
	}
	o := computerRuntimeObservation()
	defer o.Discard()
	task := publishComputerRuntime(f, o, "computer-first")
	in.ObservationID = task.ID
	first, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || !first.Available || *first.ObservedComputerCount != 23 || first.Page.Total != 23 || len(first.List) != 10 {
		t.Fatal(first, e)
	}
	in.PageIdx = 2
	second, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || len(second.List) != 10 || second.List[0].GUID <= first.List[9].GUID {
		t.Fatal(second, e)
	}
	in.PageIdx = 1
	in.Search = "computer-23"
	filtered, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || filtered.Page.Total != 1 || *filtered.ObservedComputerCount != 23 {
		t.Fatal(filtered, e)
	}
	in.Search = "not present"
	zero, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || !zero.Available || zero.Page.Total != 0 || *zero.ObservedComputerCount != 23 {
		t.Fatal(zero, e)
	}
	in.Search = ""
	empty := directoryV3RuntimeObservation()
	defer empty.Discard()
	latest := publishComputerRuntime(f, empty, "computer-no-computers")
	pinned, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || pinned.ObservationID != task.ID || *pinned.ObservedComputerCount != 23 {
		t.Fatal("pin fell forward", e)
	}
	in.ObservationID = ""
	observedZero, e := readComputerRuntime(f, f.store, in, []string{f.id})
	if e != nil || !observedZero.Available || observedZero.ObservationID != latest.ID || *observedZero.ObservedComputerCount != 0 {
		t.Fatal(observedZero, e)
	}
	disabled := domains.New(directoryV3RuntimeWithGates(f, false, false))
	if got, e := readComputerRuntime(f, disabled, in, []string{f.id}); e != nil || !got.Available {
		t.Fatal("producer flags blocked stored reader", e)
	}
	for _, tc := range []struct {
		pin, revision string
		scope         []string
		code          string
	}{{task.ID, "999", nil, "not_found"}, {task.ID, "999", []string{f.id}, "selection_changed"}, {"missing", "2", []string{f.id}, "directory_observation_unavailable"}} {
		q := in
		q.ObservationID, q.ExpectedRevision = tc.pin, tc.revision
		out, e := readComputerRuntime(f, f.store, q, tc.scope)
		var p *domains.Error
		if !errors.As(e, &p) || p.Code != tc.code || out.Available {
			t.Fatal(tc.code, e)
		}
	}
}
func TestComputerAssetsV3ExcludedUserCorruptionInvalidatesFullRead(t *testing.T) {
	f := directoryV3RuntimeFixture(t)
	grantDirectoryV3(f)
	o := computerRuntimeObservation()
	defer o.Discard()
	task := publishComputerRuntime(f, o, "computer-hidden-corruption")
	var body []byte
	if e := f.conn.QueryRow(context.Background(), `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&body); e != nil {
		t.Fatal(e)
	}
	// Corrupt only the excluded user row, recalculate digest, and preserve count.
	marker := []byte(`"objectGUID":"ffffffff-4455-6677-8899-aabbccddeeff"`)
	at := bytes.Index(body, marker)
	if at < 0 {
		t.Fatal("missing excluded user")
	}
	body = append(append([]byte{}, body[:at]...), bytes.Replace(body[at:], []byte(`"primaryGroupIDBytes":"NTEz"`), []byte(`"primaryGroupIDBytes":"@@@"`), 1)...)
	sum := sha256.Sum256(body)
	f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
	f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3 WHERE task_id=$1`, task.ID, body, hex.EncodeToString(sum[:]))
	f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
	clear(body)
	q := computerRuntimeFilter(f, task.ID)
	q.Search = "no match"
	out, e := readComputerRuntime(f, f.store, q, []string{f.id})
	var p *domains.Error
	if !errors.As(e, &p) || p.Code != "directory_observation_unavailable" || out.Available || out.List != nil {
		t.Fatal("hidden corrupt row bypassed full validation", e)
	}
}
