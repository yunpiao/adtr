//go:build integration

package domains_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
)

// The migrator, engine, authorities, ledger, observation guard and stored read
// below execute against PostgreSQL. Only these tests' observation source is
// synthetic; these cases do not assert TLS/LDAP transport execution.
func directoryV2RuntimeFixture(t *testing.T) *directoryUseFixture {
	t.Helper()
	f := fixtureForDirectoryUse(t, true, nil)
	f.runtimeEnvironment = runtimeForExecutorEnvironment(t)
	f.runtime = directoryV2RuntimeWithGates(f, true, true)
	f.store = domains.New(f.runtime)
	installDirectoryV2RuntimeKind(t, f, f.store.DirectoryV2Kind())
	return f
}

func directoryV2RuntimeWithGates(f *directoryUseFixture, master, v2 bool) *domainconfig.Runtime {
	f.t.Helper()
	env := maps.Clone(f.runtimeEnvironment)
	env["ADTR_DIRECTORY_READ_ENABLED"], env["ADTR_DIRECTORY_READ_V2_ENABLED"] = strconv.FormatBool(master), strconv.FormatBool(v2)
	runtime, err := domainconfig.Load(func(k string) string { return env[k] })
	if err != nil {
		f.t.Fatal(err)
	}
	return runtime
}

func installDirectoryV2RuntimeKind(t *testing.T, f *directoryUseFixture, v2 tasks.Kind) {
	t.Helper()
	var err error
	f.engine, err = tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), f.store.AccountKind(), f.store.DirectoryKind(), v2), auth.NewTaskAuthorizer(), f.schemaVersion)
	if err != nil {
		t.Fatal(err)
	}
}

func submitDirectoryV2Runtime(f *directoryUseFixture, key string) tasks.Task {
	f.t.Helper()
	var task tasks.Task
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.SubmitDirectoryV2Tx(ctx, tx, f.engine, f.p, domains.DirectoryInput{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialGeneration: "2", IdempotencyKey: key})
		task = out.Task
		return err
	})
	return task
}

func directoryV2RuntimeObservation() ldapconnection.DirectoryV2Observation {
	now := time.Now().UTC()
	return ldapconnection.DirectoryV2Observation{Source: ldapconnection.DirectorySource{ServerName: "dc1.example.test", DCHostName: "dc1.example.test", Domain: "example.test", NamingContext: "DC=example,DC=test", StartedAt: now, CompletedAt: now.Add(time.Second), ElapsedMilliseconds: 1000, Pages: 1}, Objects: []directoryassets.StoredObjectV2{{Base: directoryassets.Object{GUID: "00112233-4455-6677-8899-aabbccddeeff", DN: "CN=Synthetic,DC=example,DC=test", Kind: directoryassets.User, Classes: []string{"top", "user"}}, Supplemental: directoryassets.RawSupplementalV2{ObjectSIDBytes: []byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0}, MailBytes: []byte(" User\x00😀@example.test "), DescriptionBytes: [][]byte{[]byte("line\r\n\t\x00<script>😀")}, WhenCreatedBytes: []byte("00010101000000.0Z")}}}}
}

func directoryV2RuntimeList(f *directoryUseFixture, s *domains.Store, pin string, page int) (domains.DirectoryV2List, error) {
	f.t.Helper()
	var out domains.DirectoryV2List
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.DirectoryV2ListTx(ctx, tx, f.p.TenantID, []string{f.id}, domains.DirectoryFilter{DomainID: f.id, ObservationID: pin, PageIdx: page, PageSize: 25})
		return err
	})
	return out, err
}

func TestDirectoryV2RuntimePurposeAdmissionReceiptCancellationAndGates(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	in := domains.DirectoryInput{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialGeneration: "2", IdempotencyKey: "separate-profile"}
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		_, err := f.store.SubmitDirectoryV2Tx(ctx, tx, f.engine, f.p, in)
		return err
	})
	if !errors.Is(err, tasks.ErrAuthorization) {
		t.Fatal("v1 and connection grants implied v2 authority", err)
	}
	var n int
	if err = f.conn.QueryRow(context.Background(), `SELECT count(*) FROM adtr.tasks WHERE kind='domain.directory_read.v2'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("denied admission left a task", n, err)
	}
	grantDirectoryV2(f)
	task := submitDirectoryV2Runtime(f, in.IdempotencyKey)
	var payload map[string]json.RawMessage
	if json.Unmarshal(task.Payload, &payload) != nil || len(payload) != 9 || string(payload["dictionaryVersion"]) != "2" {
		t.Fatal("v2 admission omitted or widened immutable payload")
	}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		replay, err := f.store.SubmitDirectoryV2Tx(ctx, tx, f.engine, f.p, in)
		if err != nil || !replay.Replayed || replay.Task.ID != task.ID {
			t.Fatal("matching v2 intent did not replay", err)
		}
		receipt, err := f.store.DirectoryV2ReceiptTx(ctx, tx, f.engine, f.p, f.id, in.IdempotencyKey)
		if err != nil || receipt.ID != task.ID || receipt.Kind != domains.DirectoryV2KindName {
			t.Fatal("v2 receipt lost profile", err)
		}
		return nil
	})
	for _, operation := range []func(context.Context, pgx.Tx) error{
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.store.DirectoryReceiptTx(ctx, tx, f.engine, f.p, f.id, in.IdempotencyKey)
			return err
		},
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.store.CancelDirectoryTx(ctx, tx, f.engine, f.p, task.ID, []string{f.id})
			return err
		},
	} {
		var failure *domains.Error
		if err := f.attempt(operation); !errors.As(err, &failure) || failure.Status != 404 {
			t.Fatal("v1 receipt/cancel accepted v2", err)
		}
	}
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		disabled := domains.New(directoryV2RuntimeWithGates(f, flags[0], flags[1]))
		err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
			_, err := disabled.SubmitDirectoryV2Tx(ctx, tx, f.engine, f.p, in)
			return err
		})
		var failure *domains.Error
		if !errors.As(err, &failure) || failure.Code != "directory_read_disabled" {
			t.Fatal("a missing gate admitted collection", flags, err)
		}
		page, err := directoryV2RuntimeList(f, disabled, "", 1)
		if err != nil || page.Available || page.DictionaryVersion != 2 || page.List == nil {
			t.Fatal("disabled collector blocked authorized empty read", err)
		}
		f.tx(func(ctx context.Context, tx pgx.Tx) error {
			_, err := disabled.DirectoryV2ReceiptTx(ctx, tx, f.engine, f.p, f.id, in.IdempotencyKey)
			return err
		})
	}
	disabled := domains.New(nil)
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		_, err := disabled.CancelDirectoryV2Tx(ctx, tx, f.engine, f.p, task.ID, []string{f.id})
		return err
	})
	if n, err := disabled.ReconcileReservedDirectoryUses(context.Background(), f.config, 100); err != nil || n != 0 {
		t.Fatal("v1 maintenance consumed v2 reservation", n, err)
	}
	if n, err := disabled.ReconcileReservedDirectoryV2Uses(context.Background(), f.config, 100); err != nil || n != 1 {
		t.Fatal("disabled original cleanup failed", n, err)
	}
	if err := f.conn.QueryRow(context.Background(), `SELECT count(*) FROM adtr.domain_audit WHERE task_id=$1 AND action IN ('domain_directory_v2_submit','domain_directory_v2_cancel')`, task.ID).Scan(&n); err != nil || n != 2 {
		t.Fatal("v2 audit actions missing or duplicated", n, err)
	}
}

func TestDirectoryV2RuntimeCanonicalLosslessPublicationAndCurrentSourceRead(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grant := grantDirectoryV2(f)
	o := directoryV2RuntimeObservation()
	defer o.Discard()
	for i := 1; i < 30; i++ {
		object := o.Objects[0]
		object.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 30-i)
		o.Objects = append(o.Objects, object)
	}
	kind := f.store.DirectoryV2Kind()
	kind.Execute = domains.PublishDirectoryV2ObservationForTest(f.store, o)
	installDirectoryV2RuntimeKind(t, f, kind)
	task := submitDirectoryV2Runtime(f, "lossless-snapshot")
	if page, err := directoryV2RuntimeList(f, f.store, "", 1); err != nil || page.Available {
		t.Fatal("queued task became an observation", err)
	}
	if _, err := f.engine.RunOne(context.Background(), "v2-publisher"); err != nil {
		t.Fatal(err)
	}
	page, err := directoryV2RuntimeList(f, f.store, "", 1)
	if err != nil || !page.Available || page.DictionaryVersion != 2 || page.ObservationID != task.ID || page.Page.Total != 30 || len(page.List) != 25 {
		t.Fatal("published snapshot unavailable", page.Page, err)
	}
	for i, object := range page.List {
		if object.Mail == nil || *object.Mail != " User\x00😀@example.test " || object.WhenCreated == nil || *object.WhenCreated != "0001-01-01T00:00:00Z" || object.ObjectSID == nil || *object.ObjectSID != "S-1-5-21" || len(object.Description) != 1 || object.Description[0] != "line\r\n\t\x00<script>😀" || i > 0 && object.GUID <= page.List[i-1].GUID {
			t.Fatal("PostgreSQL/public projection changed source bytes or order")
		}
	}
	second, err := directoryV2RuntimeList(f, f.store, task.ID, 2)
	if err != nil || len(second.List) != 5 || second.List[0].GUID <= page.List[24].GUID {
		t.Fatal("snapshot continuation order changed", err)
	}
	var body []byte
	var dictionary, n int
	if err := f.conn.QueryRow(context.Background(), `SELECT body,dictionary_version,(SELECT count(*) FROM adtr.domain_audit WHERE task_id=$1 AND action='domain_directory_v2_result') FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&body, &dictionary, &n); err != nil || dictionary != 2 || n != 1 || bytes.Contains(body, []byte(`\u0000`)) || bytes.Contains(body, []byte{0}) {
		t.Fatal("stored envelope/audit lacks versioned lossless encoding", dictionary, n, err)
	}
	clear(body)
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		legacy, err := f.store.DirectoryListTx(ctx, tx, f.p.TenantID, []string{f.id}, domains.DirectoryFilter{DomainID: f.id, PageIdx: 1, PageSize: 25})
		if err != nil || legacy.Available || len(legacy.List) != 0 {
			t.Fatal("legacy reader fell forward to v2", err)
		}
		return nil
	})
	// Credential-use grants govern collection, not authority to read the stored
	// observation after the original executor has returned.
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
			return err
		}
		_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV2Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-after-publication"}, []string{f.id}, credentialuse.DirectoryV2Purpose)
		return err
	})
	// Collection switches do not affect stored reads. Reuse exactly the same
	// policy and trust snapshot while changing only the deployment flags.
	disabled := domains.New(directoryV2RuntimeWithGates(f, false, false))
	if page, err := directoryV2RuntimeList(f, disabled, task.ID, 1); err != nil || !page.Available {
		t.Fatal("disabled collection hid stored data", err)
	}
	f.detach()
	if page, err := directoryV2RuntimeList(f, f.store, "", 1); err != nil || page.Available {
		t.Fatal("old-binding snapshot stayed current", err)
	}
	var failure *domains.Error
	if _, err := directoryV2RuntimeList(f, f.store, task.ID, 2); !errors.As(err, &failure) || failure.Status != 409 || failure.Code != "directory_observation_unavailable" {
		t.Fatal("stale pin did not require refresh", err)
	}
}

func TestDirectoryV2RuntimeMalformedStoredObservationFailsClosed(t *testing.T) {
	for _, mode := range []string{"digest", "count", "dictionary", "noncanonical", "wrong-source", "bad-base64", "failed-task"} {
		t.Run(mode, func(t *testing.T) {
			f := directoryV2RuntimeFixture(t)
			grantDirectoryV2(f)
			o := directoryV2RuntimeObservation()
			defer o.Discard()
			kind := f.store.DirectoryV2Kind()
			kind.Execute = domains.PublishDirectoryV2ObservationForTest(f.store, o)
			installDirectoryV2RuntimeKind(t, f, kind)
			task := submitDirectoryV2Runtime(f, "corruption")
			if _, err := f.engine.RunOne(context.Background(), "v2-publisher"); err != nil {
				t.Fatal(err)
			}
			var body []byte
			var digest string
			count, version := 1, 2
			if err := f.conn.QueryRow(context.Background(), `SELECT body,sha256 FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&body, &digest); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "digest":
				digest = strings.Repeat("0", 64)
			case "count":
				count = 0
			case "dictionary":
				version = 1
			case "noncanonical":
				body = append(body, ' ')
			case "wrong-source":
				body = bytes.ReplaceAll(body, []byte("dc1.example.test"), []byte("dc2.example.test"))
			case "bad-base64":
				body = bytes.Replace(body, []byte(`"objectSidBytes":"AQE`), []byte(`"objectSidBytes":"@@@`), 1)
			case "failed-task":
				f.exec(`UPDATE adtr.tasks SET state='failed' WHERE task_id=$1`, task.ID)
			}
			if mode != "digest" {
				sum := sha256.Sum256(body)
				digest = hex.EncodeToString(sum[:])
			}
			// This isolated corruption fixture explicitly bypasses immutability
			// to test read-time defence. Normal SQL mutation remains prohibited.
			f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
			f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3,object_count=$4,dictionary_version=$5 WHERE task_id=$1`, task.ID, body, digest, count, version)
			f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
			clear(body)
			page, err := directoryV2RuntimeList(f, f.store, task.ID, 1)
			var failure *domains.Error
			if !errors.As(err, &failure) || failure.Code != "directory_observation_unavailable" || page.List != nil || page.Available {
				t.Fatal("corrupt or unsuccessful snapshot returned partial rows", err)
			}
		})
	}
}

func TestDirectoryV2RuntimeRevokedAuthorityCannotPublish(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grant := grantDirectoryV2(f)
	o := directoryV2RuntimeObservation()
	defer o.Discard()
	kind := f.store.DirectoryV2Kind()
	worker := domains.PublishDirectoryV2ObservationForTest(f.store, o)
	kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		original := ex.WithTx
		calls := 0
		ex.WithTx = func(ctx context.Context, version int64, work tasks.FencedWork) (int64, error) {
			v, err := original(ctx, version, work)
			calls++
			if calls == 1 && err == nil {
				f.tx(func(ctx context.Context, tx pgx.Tx) error {
					if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
						return err
					}
					_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV2Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-before-publication"}, []string{f.id}, credentialuse.DirectoryV2Purpose)
					return err
				})
			}
			return v, err
		}
		return worker(ctx, ex)
	}
	installDirectoryV2RuntimeKind(t, f, kind)
	task := submitDirectoryV2Runtime(f, "revoked-publication")
	if _, err := f.engine.RunOne(context.Background(), "original-v2-owner"); err != nil {
		t.Fatal(err)
	}
	var n int
	var state string
	if err := f.conn.QueryRow(context.Background(), `SELECT state,(SELECT count(*) FROM adtr.domain_directory_observations WHERE task_id=$1) FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID).Scan(&state, &n); err != nil || state != "quiesced" || n != 0 {
		t.Fatal("revocation allowed publication or blocked original return", state, n, err)
	}
}

func TestDirectoryV2RuntimeObservedEmptyRemainsAvailable(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grantDirectoryV2(f)
	o := directoryV2RuntimeObservation()
	directoryassets.ClearStoredObjectsV2(o.Objects)
	o.Objects = []directoryassets.StoredObjectV2{}
	defer o.Discard()
	kind := f.store.DirectoryV2Kind()
	kind.Execute = domains.PublishDirectoryV2ObservationForTest(f.store, o)
	installDirectoryV2RuntimeKind(t, f, kind)
	task := submitDirectoryV2Runtime(f, "empty-observation")
	if _, err := f.engine.RunOne(context.Background(), "empty-v2-owner"); err != nil {
		t.Fatal(err)
	}
	page, err := directoryV2RuntimeList(f, f.store, task.ID, 1)
	if err != nil || !page.Available || page.DictionaryVersion != 2 || page.List == nil || len(page.List) != 0 || page.Page.Total != 0 || page.ObservationID != task.ID {
		t.Fatal("successful empty collection became unavailable", err)
	}
}

func TestDirectoryV2RuntimeStagedCancellationNeverBecomesVisible(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grantDirectoryV2(f)
	o := directoryV2RuntimeObservation()
	defer o.Discard()
	kind := f.store.DirectoryV2Kind()
	publish := domains.PublishDirectoryV2ObservationForTest(f.store, o)
	kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		out := publish(ctx, ex)
		if out.State != tasks.Succeeded {
			t.Fatal("synthetic staging failed", out)
		}
		if page, err := directoryV2RuntimeList(f, f.store, "", 1); err != nil || page.Available {
			t.Fatal("staged observation visible before Finish", err)
		}
		f.tx(func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.store.CancelDirectoryV2Tx(ctx, tx, f.engine, f.p, ex.Task.ID, []string{f.id})
			return err
		})
		return out
	}
	installDirectoryV2RuntimeKind(t, f, kind)
	task := submitDirectoryV2Runtime(f, "cancel-after-staging")
	if _, err := f.engine.RunOne(context.Background(), "staged-v2-owner"); err != nil {
		t.Fatal(err)
	}
	var state, use string
	if err := f.conn.QueryRow(context.Background(), `SELECT t.state,u.state FROM adtr.tasks t JOIN adtr.domain_directory_task_uses u USING(task_id) WHERE t.task_id=$1`, task.ID).Scan(&state, &use); err != nil || state != "cancelled" || use != "quiesced" {
		t.Fatal("cancellation or original cleanup failed", state, use, err)
	}
	page, err := directoryV2RuntimeList(f, f.store, "", 1)
	if err != nil || page.Available {
		t.Fatal("cancelled task exposed staged rows", err)
	}
	var failure *domains.Error
	if _, err := directoryV2RuntimeList(f, f.store, task.ID, 1); !errors.As(err, &failure) || failure.Code != "directory_observation_unavailable" {
		t.Fatal("cancelled explicit pin accepted", err)
	}
}
