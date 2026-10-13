//go:build integration

package domains_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/auth"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
	"maps"
	"strconv"
	"testing"
	"time"
)

// Real PostgreSQL lifecycle guard tests; only the observation bytes are synthetic.
func directoryV4RuntimeFixture(t *testing.T) *directoryUseFixture {
	t.Helper()
	f := fixtureForDirectoryUse(t, true, nil)
	f.runtimeEnvironment = runtimeForExecutorEnvironment(t)
	f.runtime = directoryV4RuntimeWithGates(f, true, true)
	f.store = domains.New(f.runtime)
	installDirectoryV4RuntimeKind(t, f, f.store.DirectoryV4Kind())
	return f
}

func directoryV4RuntimeWithGates(f *directoryUseFixture, master, v4 bool) *domainconfig.Runtime {
	f.t.Helper()
	env := maps.Clone(f.runtimeEnvironment)
	env["ADTR_DIRECTORY_READ_V2_ENABLED"] = "false"
	env["ADTR_DIRECTORY_READ_ENABLED"], env["ADTR_DIRECTORY_READ_V4_ENABLED"] = strconv.FormatBool(master), strconv.FormatBool(v4)
	runtime, err := domainconfig.Load(func(k string) string { return env[k] })
	if err != nil {
		f.t.Fatal(err)
	}
	return runtime
}

func installDirectoryV4RuntimeKind(t *testing.T, f *directoryUseFixture, v4 tasks.Kind) {
	t.Helper()
	var err error
	f.engine, err = tasks.New(f.config, tasks.ProductionRegistry(f.store.Kind(), f.store.AccountKind(), f.store.DirectoryKind(), v4), auth.NewTaskAuthorizer(), f.schemaVersion)
	if err != nil {
		t.Fatal(err)
	}
}

func submitDirectoryV4Runtime(f *directoryUseFixture, key string) tasks.Task {
	f.t.Helper()
	var task tasks.Task
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.SubmitDirectoryV4Tx(ctx, tx, f.engine, f.p, domains.DirectoryInput{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialGeneration: "2", IdempotencyKey: key})
		task = out.Task
		return err
	})
	return task
}

func directoryV4RuntimeObservation() ldapconnection.DirectoryV4Observation {
	now := time.Now().UTC()
	return ldapconnection.DirectoryV4Observation{Source: ldapconnection.DirectorySource{ServerName: "dc1.example.test", DCHostName: "dc1.example.test", Domain: "example.test", NamingContext: "DC=example,DC=test", StartedAt: now, CompletedAt: now.Add(time.Second), ElapsedMilliseconds: 1000, Pages: 1}, Objects: []directoryassets.StoredObjectV4{{Base: directoryassets.Object{GUID: "00112233-4455-6677-8899-aabbccddeeff", DN: "CN=Synthetic,DC=example,DC=test", Kind: directoryassets.Group, Classes: []string{"group", "top"}}, Membership: directoryassets.RawMembershipV4{MemberBytes: [][]byte{[]byte("CN=member,DC=foreign,DC=test"), []byte("CN=raw\x00😀")}}, Supplemental: directoryassets.RawSupplementalV2{ObjectSIDBytes: []byte{1, 1, 0, 0, 0, 0, 0, 5, 21, 0, 0, 0}, MailBytes: []byte(" User\x00😀@example.test "), DescriptionBytes: [][]byte{[]byte("line\r\n\t\x00<script>😀")}, WhenCreatedBytes: []byte("00010101000000.0Z")}, Identity: directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("513"), ServicePrincipalNameBytes: [][]byte{[]byte("HOST/Z"), []byte("host/a\x00😀")}}}}}
}

func directoryV4RuntimeList(f *directoryUseFixture, s *domains.Store, pin string, page int) (domains.DirectoryV4List, error) {
	f.t.Helper()
	var out domains.DirectoryV4List
	err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.DirectoryV4ListTx(ctx, tx, f.p.TenantID, []string{f.id}, domains.DirectoryFilter{DomainID: f.id, ObservationID: pin, PageIdx: page, PageSize: 25})
		return err
	})
	return out, err
}

func TestDirectoryV4RuntimeCanonicalLosslessPublicationAndCurrentSourceRead(t *testing.T) {
	f := directoryV4RuntimeFixture(t)
	grant := grantDirectoryV4(f)
	o := directoryV4RuntimeObservation()
	defer o.Discard()
	for i := 1; i < 30; i++ {
		object := o.Objects[0]
		object.Base.GUID = fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 30-i)
		o.Objects = append(o.Objects, object)
	}
	kind := f.store.DirectoryV4Kind()
	kind.Execute = domains.PublishDirectoryV4ObservationForTest(f.store, o)
	installDirectoryV4RuntimeKind(t, f, kind)
	task := submitDirectoryV4Runtime(f, "lossless-snapshot")
	if page, err := directoryV4RuntimeList(f, f.store, "", 1); err != nil || page.Available {
		t.Fatal("queued task became an observation", err)
	}
	if _, err := f.engine.RunOne(context.Background(), "v4-publisher"); err != nil {
		t.Fatal(err)
	}
	page, err := directoryV4RuntimeList(f, f.store, "", 1)
	if err != nil || !page.Available || page.DictionaryVersion != 4 || page.ObservationID != task.ID || page.Page.Total != 30 || len(page.List) != 25 {
		t.Fatal("published snapshot unavailable", page.Page, err)
	}
	for i, object := range page.List {
		if object.MemberReadState != "returned" || object.ReturnedMemberCount == nil || *object.ReturnedMemberCount != 2 || len(object.RawMember) != 2 || object.RawMember[1] != "CN=raw\x00😀" {
			t.Fatal("raw member projection changed")
		}
		if object.PrimaryGroupID == nil || *object.PrimaryGroupID != 513 || len(object.ServicePrincipalName) != 2 || object.ServicePrincipalName[0] != "HOST/Z" || object.ServicePrincipalName[1] != "host/a\x00😀" {
			t.Fatal("v4 raw identity projection changed")
		}
		if object.Mail == nil || *object.Mail != " User\x00😀@example.test " || object.WhenCreated == nil || *object.WhenCreated != "0001-01-01T00:00:00Z" || object.ObjectSID == nil || *object.ObjectSID != "S-1-5-21" || len(object.Description) != 1 || object.Description[0] != "line\r\n\t\x00<script>😀" || i > 0 && object.GUID <= page.List[i-1].GUID {
			t.Fatal("PostgreSQL/public projection changed source bytes or order")
		}
	}
	second, err := directoryV4RuntimeList(f, f.store, task.ID, 2)
	if err != nil || len(second.List) != 5 || second.List[0].GUID <= page.List[24].GUID {
		t.Fatal("snapshot continuation order changed", err)
	}
	var body []byte
	var dictionary, n int
	if err := f.conn.QueryRow(context.Background(), `SELECT body,dictionary_version,(SELECT count(*) FROM adtr.domain_audit WHERE task_id=$1 AND action='domain_directory_v4_result') FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&body, &dictionary, &n); err != nil || dictionary != 4 || n != 1 || bytes.Contains(body, []byte(`\u0000`)) || bytes.Contains(body, []byte{0}) {
		t.Fatal("stored envelope/audit lacks versioned lossless encoding", dictionary, n, err)
	}
	clear(body)
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		legacy, err := f.store.DirectoryListTx(ctx, tx, f.p.TenantID, []string{f.id}, domains.DirectoryFilter{DomainID: f.id, PageIdx: 1, PageSize: 25})
		if err != nil || legacy.Available || len(legacy.List) != 0 {
			t.Fatal("legacy reader fell forward to v4", err)
		}
		return nil
	})
	// Credential-use grants govern collection, not authority to read the stored
	// observation after the original executor has returned.
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
			return err
		}
		_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV4Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-after-publication"}, []string{f.id}, credentialuse.DirectoryV4Purpose)
		return err
	})
	// Collection switches do not affect stored reads. Reuse exactly the same
	// policy and trust snapshot while changing only the deployment flags.
	disabled := domains.New(directoryV4RuntimeWithGates(f, false, false))
	if page, err := directoryV4RuntimeList(f, disabled, task.ID, 1); err != nil || !page.Available {
		t.Fatal("disabled collection hid stored data", err)
	}
	f.detach()
	if page, err := directoryV4RuntimeList(f, f.store, "", 1); err != nil || page.Available {
		t.Fatal("old-binding snapshot stayed current", err)
	}
	var failure *domains.Error
	if _, err := directoryV4RuntimeList(f, f.store, task.ID, 2); !errors.As(err, &failure) || failure.Status != 409 || failure.Code != "directory_observation_unavailable" {
		t.Fatal("stale pin did not require refresh", err)
	}
}

func TestDirectoryV4RuntimeRevokedAuthorityCannotPublish(t *testing.T) {
	f := directoryV4RuntimeFixture(t)
	grant := grantDirectoryV4(f)
	o := directoryV4RuntimeObservation()
	defer o.Discard()
	kind := f.store.DirectoryV4Kind()
	worker := domains.PublishDirectoryV4ObservationForTest(f.store, o)
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
					_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV4Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-before-publication"}, []string{f.id}, credentialuse.DirectoryV4Purpose)
					return err
				})
			}
			return v, err
		}
		return worker(ctx, ex)
	}
	installDirectoryV4RuntimeKind(t, f, kind)
	task := submitDirectoryV4Runtime(f, "revoked-publication")
	if _, err := f.engine.RunOne(context.Background(), "original-v4-owner"); err != nil {
		t.Fatal(err)
	}
	var n int
	var state string
	if err := f.conn.QueryRow(context.Background(), `SELECT state,(SELECT count(*) FROM adtr.domain_directory_observations WHERE task_id=$1) FROM adtr.domain_directory_task_uses WHERE task_id=$1`, task.ID).Scan(&state, &n); err != nil || state != "quiesced" || n != 0 {
		t.Fatal("revocation allowed publication or blocked original return", state, n, err)
	}
}

func TestDirectoryV4RuntimeStagedCancellationNeverBecomesVisible(t *testing.T) {
	f := directoryV4RuntimeFixture(t)
	grantDirectoryV4(f)
	o := directoryV4RuntimeObservation()
	defer o.Discard()
	kind := f.store.DirectoryV4Kind()
	publish := domains.PublishDirectoryV4ObservationForTest(f.store, o)
	kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		out := publish(ctx, ex)
		if out.State != tasks.Succeeded {
			t.Fatal("synthetic staging failed", out)
		}
		if page, err := directoryV4RuntimeList(f, f.store, "", 1); err != nil || page.Available {
			t.Fatal("staged observation visible before Finish", err)
		}
		f.tx(func(ctx context.Context, tx pgx.Tx) error {
			_, err := f.store.CancelDirectoryV4Tx(ctx, tx, f.engine, f.p, ex.Task.ID, []string{f.id})
			return err
		})
		return out
	}
	installDirectoryV4RuntimeKind(t, f, kind)
	task := submitDirectoryV4Runtime(f, "cancel-after-staging")
	if _, err := f.engine.RunOne(context.Background(), "staged-v4-owner"); err != nil {
		t.Fatal(err)
	}
	var state, use string
	if err := f.conn.QueryRow(context.Background(), `SELECT t.state,u.state FROM adtr.tasks t JOIN adtr.domain_directory_task_uses u USING(task_id) WHERE t.task_id=$1`, task.ID).Scan(&state, &use); err != nil || state != "cancelled" || use != "quiesced" {
		t.Fatal("cancellation or original cleanup failed", state, use, err)
	}
	page, err := directoryV4RuntimeList(f, f.store, "", 1)
	if err != nil || page.Available {
		t.Fatal("cancelled task exposed staged rows", err)
	}
	var failure *domains.Error
	if _, err := directoryV4RuntimeList(f, f.store, task.ID, 1); !errors.As(err, &failure) || failure.Code != "directory_observation_unavailable" {
		t.Fatal("cancelled explicit pin accepted", err)
	}
}

func grantDirectoryV4(f *directoryUseFixture) string {
	f.t.Helper()
	var revision string
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.GrantUse); err != nil {
			return err
		}
		out, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/grant", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV4Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: "0", IdempotencyKey: "directory-v4-grant"}, []string{f.id}, credentialuse.DirectoryV4Purpose)
		revision = out.GrantRevision
		return err
	})
	return revision
}
