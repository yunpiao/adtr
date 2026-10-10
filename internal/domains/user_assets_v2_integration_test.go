//go:build integration

package domains_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/credentialuse"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domainconfig"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/operationaccounts"
	"github.com/yunpiao/adtr/internal/tasks"
)

// These tests exercise the real migrator, publication guard, task engine,
// source/authority serialization and immutable read against PostgreSQL. The
// observation input is synthetic; real TLS LDAP/browser evidence is separate.
func userAssetsV2Observation() ldapconnection.DirectoryV2Observation {
	o := directoryV2RuntimeObservation()
	directoryassets.ClearStoredObjectsV2(o.Objects)
	o.Objects = nil
	for i := 63; i >= 1; i-- {
		sam := fmt.Sprintf("Synthetic %02d", i)
		sid := []byte{1, 1, 0, 0, 0, 0, 0, 5, 0, 0, 0, 0}
		binary.LittleEndian.PutUint32(sid[8:], uint32(1000+i))
		zero := uint32(0)
		row := directoryassets.StoredObjectV2{Base: directoryassets.Object{GUID: fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", i), DN: fmt.Sprintf("CN=Synthetic %02d,DC=example,DC=test", i), Kind: directoryassets.User, Classes: []string{"top", "user"}, SAMAccountName: &sam, UserAccountControl: &zero}, Supplemental: directoryassets.RawSupplementalV2{ObjectSIDBytes: sid, MailBytes: []byte(fmt.Sprintf("user%02d@example.test", i)), DescriptionBytes: [][]byte{[]byte("description-only")}, WhenCreatedBytes: []byte("00010101000000.0Z")}}
		switch i {
		case 1:
			row.Base.SAMAccountName = nil
			row.Supplemental = directoryassets.RawSupplementalV2{}
		case 2:
			row.Supplemental.MailBytes = []byte(" Space\x00\r\n\u200b😀 <script>%_*?'\"\\()[]+& ")
			row.Supplemental.DescriptionBytes = [][]byte{[]byte("line\r\n\t\x00<script>😀")}
		case 55:
			name := "UniqueSAM"
			row.Base.SAMAccountName = &name
		case 56:
			binary.LittleEndian.PutUint32(row.Supplemental.ObjectSIDBytes[8:], 56789)
		case 57:
			row.Supplemental.MailBytes = []byte(" UniqueMail@example.test ")
		case 58:
			row.Base.DN = "CN=UniqueDN\\,plus\\+x,DC=example,DC=test"
		case 59, 60:
			name := "same-observed-sam"
			row.Base.SAMAccountName = &name
			row.Base.DN = "CN=SameObservedDN,DC=example,DC=test"
		}
		o.Objects = append(o.Objects, row)
	}
	for i, kind := range []directoryassets.Kind{directoryassets.Group, directoryassets.Computer} {
		classes := []string{"group", "top"}
		if kind == directoryassets.Computer {
			classes = []string{"computer", "top", "user"}
		}
		o.Objects = append(o.Objects, directoryassets.StoredObjectV2{Base: directoryassets.Object{GUID: fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", 100+i), DN: "CN=Decoy,DC=example,DC=test", Kind: kind, Classes: classes}})
	}
	return o
}

func publishUserAssetsV2(f *directoryUseFixture, o ldapconnection.DirectoryV2Observation, key string) tasks.Task {
	f.t.Helper()
	kind := f.store.DirectoryV2Kind()
	kind.Execute = domains.PublishDirectoryV2ObservationForTest(f.store, o)
	installDirectoryV2RuntimeKind(f.t, f, kind)
	task := submitDirectoryV2Runtime(f, key)
	if _, err := f.engine.RunOne(context.Background(), "user-asset-publisher"); err != nil {
		f.t.Fatal(err)
	}
	var state string
	if err := f.conn.QueryRow(context.Background(), `SELECT state FROM adtr.tasks WHERE task_id=$1`, task.ID).Scan(&state); err != nil || state != "succeeded" {
		f.t.Fatal("real publication did not succeed", state, err)
	}
	return task
}

func userAssetsV2Filter(f *directoryUseFixture, pin, search string, page, size int) domains.UserAssetsV2Filter {
	return domains.UserAssetsV2Filter{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: pin, Search: search, PageIdx: page, PageSize: size}
}
func userAssetsV2List(f *directoryUseFixture, s *domains.Store, filter domains.UserAssetsV2Filter) (out domains.UserAssetsV2List, err error) {
	f.t.Helper()
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.UserAssetsV2ListTx(ctx, tx, f.p.TenantID, []string{f.id}, filter)
		return err
	})
	return
}
func userAssetV2Detail(f *directoryUseFixture, s *domains.Store, pin, guid string) (out domains.UserAssetV2Detail, err error) {
	f.t.Helper()
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		var err error
		out, err = s.UserAssetV2DetailTx(ctx, tx, f.p.TenantID, []string{f.id}, domains.UserAssetV2Input{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialRevision: "2", ObservationID: pin, ObjectGUID: guid})
		return err
	})
	return
}
func requireUserAssetsV2IntegrationError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var failure *domains.Error
	if !errors.As(err, &failure) || failure.Status != status || failure.Code != code {
		t.Fatalf("expected bounded %d %s, got %v", status, code, err)
	}
}
func userAssetsV2Counts(f *directoryUseFixture) [4]int {
	f.t.Helper()
	var out [4]int
	if err := f.conn.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM adtr.tasks),(SELECT count(*) FROM adtr.domain_directory_task_uses),(SELECT count(*) FROM adtr.domain_directory_observations),(SELECT count(*) FROM adtr.domain_audit)`).Scan(&out[0], &out[1], &out[2], &out[3]); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func TestUserAssetsV2PublishedSearchPaginationExactDetailAndReadOnly(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
	if err != nil || out.Available || out.List == nil || len(out.List) != 0 || out.Page.Total != 0 || out.Selection.DomainID != f.id || out.Selection.Revision != "2" || out.Selection.CredentialRevision != "2" {
		t.Fatal("absence is not a selected unavailable result", err)
	}
	grantDirectoryV2(f)
	o := userAssetsV2Observation()
	defer o.Discard()
	task := publishUserAssetsV2(f, o, "user-assets-data")
	counts := userAssetsV2Counts(f)
	first, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
	if err != nil || !first.Available || first.ObservationID != task.ID || first.Page.Total != 63 || first.Page.Pages != 7 || len(first.List) != 10 || first.Source == nil || first.Source.Domain != "example.test" {
		t.Fatal("published user first page incorrect", first.Page, err)
	}
	for _, size := range []int{10, 20, 30, 40, 50} {
		for _, page := range []int{1, 2, 7, 10000} {
			out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, task.ID, "", page, size))
			start := (page - 1) * size
			if err != nil || out.Page.Total != 63 || out.Page.Pages != (63+size-1)/size || len(out.List) != min(size, max(0, 63-start)) {
				t.Fatal("wrong user total or remainder", out.Page, err)
			}
			for i, row := range out.List {
				if row.Kind != directoryassets.User || row.GUID != fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", start+i+1) {
					t.Fatal("class inheritance or GUID order changed")
				}
			}
		}
	}
	for _, tc := range []struct {
		query  string
		number int
	}{{"uNiQuEsAm", 55}, {"s-1-5-56789", 56}, {" UniqueMail@", 57}, {"uniquedn\\,plus\\+", 58}, {"\x00", 2}, {`%_*?'"\()[]+&`, 2}} {
		out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, task.ID, tc.query, 1, 10))
		if err != nil || out.Page.Total != 1 || len(out.List) != 1 || out.List[0].GUID != fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", tc.number) {
			t.Fatal("server filter did not find exact raw late-page field", err)
		}
	}
	for _, search := range []string{"no-such-user", "description-only", "00000037-4455-6677-8899-aabbccddeeff", `\u0000`} {
		out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, task.ID, search, 1, 10))
		if err != nil || !out.Available || out.ObservationID != task.ID || out.Page.Total != 0 || len(out.List) != 0 {
			t.Fatal("empty search lost available evidence or searched an excluded field", err)
		}
	}
	for _, number := range []int{1, 2, 55, 56, 57, 58, 59, 60} {
		guid := fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", number)
		detail, err := userAssetV2Detail(f, f.store, task.ID, guid)
		var expected directoryassets.PublicObjectV2
		for _, stored := range o.Objects {
			if stored.Base.GUID == guid {
				expected, err = directoryassets.ProjectV2(stored)
				if err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		if err != nil || detail.DictionaryVersion != 2 || detail.Selection != first.Selection || detail.ObservationID != task.ID || !reflect.DeepEqual(detail.Object, expected) || !reflect.DeepEqual(detail.Source, *first.Source) {
			t.Fatal("detail does not equal the pinned stored object", err)
		}
		if number == 1 && (detail.Object.SAMAccountName != nil || detail.Object.ObjectSID != nil || detail.Object.Mail != nil || detail.Object.Description != nil || detail.Object.WhenCreated != nil || detail.Object.UserAccountControl == nil || *detail.Object.UserAccountControl != 0) {
			t.Fatal("null and observed zero changed")
		}
	}
	for _, number := range []int{64, 100, 101} {
		out, err := userAssetV2Detail(f, f.store, task.ID, fmt.Sprintf("%08x-4455-6677-8899-aabbccddeeff", number))
		requireUserAssetsV2IntegrationError(t, err, 404, "not_found")
		if !reflect.DeepEqual(out, domains.UserAssetV2Detail{}) {
			t.Fatal("missing/non-user detail disclosed data")
		}
	}
	if userAssetsV2Counts(f) != counts {
		t.Fatal("stored list/search/detail caused a task, use, observation or domain mutation")
	}
}

func TestUserAssetsV2PinnedPaginationDetailAndNewestEmptyNeverFallback(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grantDirectoryV2(f)
	o := userAssetsV2Observation()
	defer o.Discard()
	old := publishUserAssetsV2(f, o, "old-users")
	page, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
	if err != nil || page.ObservationID != old.ID {
		t.Fatal(err)
	}
	newer := directoryV2RuntimeObservation()
	defer newer.Discard()
	newer.Objects[0].Base.GUID = page.List[0].GUID
	name := "newer same GUID"
	newer.Objects[0].Base.SAMAccountName = &name
	fresh := publishUserAssetsV2(f, newer, "new-user")
	second, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, old.ID, "", 2, 10))
	if err != nil || second.ObservationID != old.ID || second.Page.Total != 63 || len(second.List) != 10 || second.List[0].GUID != "0000000b-4455-6677-8899-aabbccddeeff" {
		t.Fatal("pinned continuation silently upgraded", err)
	}
	oldDetail, err := userAssetV2Detail(f, f.store, old.ID, page.List[0].GUID)
	if err != nil || oldDetail.Object.SAMAccountName != nil {
		t.Fatal("same GUID overrode old pin", err)
	}
	newDetail, err := userAssetV2Detail(f, f.store, fresh.ID, page.List[0].GUID)
	if err != nil || newDetail.Object.SAMAccountName == nil || *newDetail.Object.SAMAccountName != name {
		t.Fatal("exact new observation unavailable", err)
	}
	latest, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
	if err != nil || latest.ObservationID != fresh.ID || latest.Page.Total != 1 {
		t.Fatal("refresh did not select newest", err)
	}
	empty := directoryV2RuntimeObservation()
	directoryassets.ClearStoredObjectsV2(empty.Objects)
	empty.Objects = []directoryassets.StoredObjectV2{}
	defer empty.Discard()
	emptyTask := publishUserAssetsV2(f, empty, "new-empty")
	latest, err = userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
	if err != nil || !latest.Available || latest.ObservationID != emptyTask.ID || latest.Page.Total != 0 || len(latest.List) != 0 {
		t.Fatal("newest empty fell back to old evidence", err)
	}
	if _, err := userAssetV2Detail(f, f.store, old.ID, page.List[0].GUID); err != nil {
		t.Fatal("eligible older pin was discarded", err)
	}
}

func removeUserAssetsV2DomainAccount(f *directoryUseFixture) {
	f.t.Helper()
	f.detach()
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.MutateAccount); err != nil {
			return err
		}
		_, err := operationaccounts.New(f.runtime).MutateTx(ctx, tx, f.p, "/delete", operationaccounts.Input{AccountID: f.account, ExpectedRevision: "1", ConfirmAccountID: f.account, IdempotencyKey: "remove-completed-account"}, []string{f.id})
		return err
	})
}

func TestUserAssetsV2CurrentScopeSelectionAndPolicyPrecedence(t *testing.T) {
	for _, mode := range []string{"revision", "credential", "no-scope", "wrong-tenant", "inactive", "deleted", "binding", "policy"} {
		t.Run(mode, func(t *testing.T) {
			f := directoryV2RuntimeFixture(t)
			grantDirectoryV2(f)
			o := userAssetsV2Observation()
			defer o.Discard()
			task := publishUserAssetsV2(f, o, "current-scope")
			filter := userAssetsV2Filter(f, task.ID, "", 1, 10)
			s := f.store
			allowed := []string{f.id}
			tenant := f.p.TenantID
			status, code := 409, "selection_changed"
			switch mode {
			case "revision":
				filter.ExpectedRevision = "1"
			case "credential":
				filter.ExpectedCredentialRevision = "1"
			case "no-scope":
				allowed = nil
				filter.ExpectedRevision = "9"
				status, code = 404, "not_found"
			case "wrong-tenant":
				tenant = "another-tenant"
				filter.ExpectedRevision = "9"
				status, code = 404, "not_found"
			case "inactive":
				removeUserAssetsV2DomainAccount(f)
				f.exec(`UPDATE adtr.resource_domains SET active=false WHERE tenant_id=$1 AND id=$2`, tenant, f.id)
				filter.ExpectedRevision = "9"
				status, code = 404, "not_found"
			case "deleted":
				removeUserAssetsV2DomainAccount(f)
				f.tx(func(ctx context.Context, tx pgx.Tx) error {
					_, err := f.store.DeleteTx(ctx, tx, f.p, domains.Input{DomainID: f.id, ExpectedRevision: "3", ConfirmDomain: "example.test"})
					return err
				})
				status, code = 404, "not_found"
			case "binding":
				f.detach()
			case "policy":
				env := maps.Clone(f.runtimeEnvironment)
				path := filepath.Join(t.TempDir(), "policy.json")
				if err := os.WriteFile(path, []byte(`{"version":1,"targets":[{"tenantId":"fixture","domain":"example.test","serverNames":["dc1.example.test"],"cidrs":["10.20.0.0/25"]}]}`), 0600); err != nil {
					t.Fatal(err)
				}
				env["ADTR_LDAP_EGRESS_POLICY_FILE"] = path
				runtime, err := domainconfig.Load(func(k string) string { return env[k] })
				if err != nil {
					t.Fatal(err)
				}
				s = domains.New(runtime)
				code = "directory_observation_unavailable"
			}
			err := f.attempt(func(ctx context.Context, tx pgx.Tx) error {
				out, err := s.UserAssetsV2ListTx(ctx, tx, tenant, allowed, filter)
				if !reflect.DeepEqual(out, domains.UserAssetsV2List{}) {
					t.Fatal("denial disclosed list/source/count")
				}
				return err
			})
			requireUserAssetsV2IntegrationError(t, err, status, code)
			err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
				out, err := s.UserAssetV2DetailTx(ctx, tx, tenant, allowed, domains.UserAssetV2Input{DomainID: f.id, ExpectedRevision: filter.ExpectedRevision, ExpectedCredentialRevision: filter.ExpectedCredentialRevision, ObservationID: task.ID, ObjectGUID: "ffffffff-ffff-ffff-ffff-ffffffffffff"})
				if !reflect.DeepEqual(out, domains.UserAssetV2Detail{}) {
					t.Fatal("denial disclosed detail/source")
				}
				return err
			})
			requireUserAssetsV2IntegrationError(t, err, status, code)
		})
	}
}

func TestUserAssetsV2StoredReadsSurviveProducerRevocationGatesAndDiagnostics(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grant := grantDirectoryV2(f)
	o := userAssetsV2Observation()
	defer o.Discard()
	task := publishUserAssetsV2(f, o, "independent-read")
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		if err := credentialuse.SetGovernanceContext(ctx, tx, f.p, credentialuse.RevokeUse); err != nil {
			return err
		}
		_, err := credentialuse.New().MutateForPurposeTx(ctx, tx, f.p, "/revoke", credentialuse.Input{AccountID: f.account, RoleID: "platform_admin", Purpose: credentialuse.DirectoryV2Purpose, ExpectedAccountRevision: "1", ExpectedCredentialRevision: "1", ExpectedGrantRevision: grant, IdempotencyKey: "revoke-producer"}, []string{f.id}, credentialuse.DirectoryV2Purpose)
		return err
	})
	// A newer diagnostic changes its own generation, not the directory pins.
	f.submitAccount("irrelevant-diagnostic")
	counts := userAssetsV2Counts(f)
	for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}} {
		s := domains.New(directoryV2RuntimeWithGates(f, flags[0], flags[1]))
		out, err := userAssetsV2List(f, s, userAssetsV2Filter(f, task.ID, "uniquesam", 1, 10))
		if err != nil || !out.Available || out.Page.Total != 1 {
			t.Fatal("collector authority/gates or diagnostic hid independent stored read", err)
		}
		if _, err := userAssetV2Detail(f, s, task.ID, out.List[0].GUID); err != nil {
			t.Fatal(err)
		}
		err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
			_, err := s.SubmitDirectoryV2Tx(ctx, tx, f.engine, f.p, domains.DirectoryInput{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialGeneration: "2", IdempotencyKey: "collector-remains-denied"})
			return err
		})
		requireUserAssetsV2IntegrationError(t, err, 503, "directory_read_disabled")
	}
	if userAssetsV2Counts(f) != counts {
		t.Fatal("authorized stored reads or denied collection changed business counts")
	}
}

func TestUserAssetsV2CorruptWholeObservationFailsBeforeSearchPageOrDetail(t *testing.T) {
	for _, mode := range []string{"digest", "count", "noncanonical", "duplicate-guid", "source", "empty-mail-outside-page", "bad-utf8-outside-page", "bad-base64-outside-page", "dictionary", "failed-task"} {
		t.Run(mode, func(t *testing.T) {
			f := directoryV2RuntimeFixture(t)
			grantDirectoryV2(f)
			o := userAssetsV2Observation()
			defer o.Discard()
			task := publishUserAssetsV2(f, o, "corrupt-whole")
			var body []byte
			var digest string
			count, version := len(o.Objects), 2
			if err := f.conn.QueryRow(context.Background(), `SELECT body,sha256 FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&body, &digest); err != nil {
				t.Fatal(err)
			}
			defer func() { clear(body) }()
			switch mode {
			case "digest":
				digest = strings.Repeat("0", 64)
			case "count":
				count--
			case "noncanonical":
				body = append(body, ' ')
			case "duplicate-guid":
				body = bytes.Replace(body, []byte("0000003f-4455-6677-8899-aabbccddeeff"), []byte("00000001-4455-6677-8899-aabbccddeeff"), 1)
			case "source":
				body = bytes.ReplaceAll(body, []byte("dc1.example.test"), []byte("dc2.example.test"))
			case "empty-mail-outside-page", "bad-utf8-outside-page", "bad-base64-outside-page":
				old := []byte(`"mailBytes":"` + base64.StdEncoding.EncodeToString([]byte("user63@example.test")) + `"`)
				replacement := []byte(`"mailBytes":""`)
				if mode == "bad-utf8-outside-page" {
					replacement = []byte(`"mailBytes":"/w=="`)
				}
				if mode == "bad-base64-outside-page" {
					replacement = []byte(`"mailBytes":"@@@="`)
				}
				if !bytes.Contains(body, old) {
					t.Fatal("corruption target absent")
				}
				body = bytes.Replace(body, old, replacement, 1)
			case "dictionary":
				version = 1
			case "failed-task":
				f.exec(`UPDATE adtr.tasks SET state='failed' WHERE task_id=$1`, task.ID)
			}
			if mode != "digest" {
				sum := sha256.Sum256(body)
				digest = hex.EncodeToString(sum[:])
			}
			// Isolated corruption fixture only; production immutability is kept.
			f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
			f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3,object_count=$4,dictionary_version=$5 WHERE task_id=$1`, task.ID, body, digest, count, version)
			f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
			for _, search := range []string{"", "UniqueSAM", "no-match"} {
				out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, task.ID, search, 1, 10))
				requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
				if !reflect.DeepEqual(out, domains.UserAssetsV2List{}) {
					t.Fatal("corrupt full snapshot leaked partial response")
				}
			}
			out, err := userAssetV2Detail(f, f.store, task.ID, "00000001-4455-6677-8899-aabbccddeeff")
			requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
			if !reflect.DeepEqual(out, domains.UserAssetV2Detail{}) {
				t.Fatal("corrupt unrelated row leaked valid detail")
			}
		})
	}
}

func TestUserAssetsV2StagedRunningFailedCancelledNeverVisible(t *testing.T) {
	for _, terminal := range []tasks.State{tasks.Succeeded, tasks.Failed, tasks.Cancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			f := directoryV2RuntimeFixture(t)
			grantDirectoryV2(f)
			o := userAssetsV2Observation()
			defer o.Discard()
			kind := f.store.DirectoryV2Kind()
			publish := domains.PublishDirectoryV2ObservationForTest(f.store, o)
			kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
				staged := publish(ctx, ex)
				if staged.State != tasks.Succeeded {
					t.Fatal("staging failed")
				}
				page, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
				if err != nil || page.Available {
					t.Fatal("running/staged snapshot visible", err)
				}
				_, err = userAssetV2Detail(f, f.store, ex.Task.ID, "00000001-4455-6677-8899-aabbccddeeff")
				requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
				if terminal == tasks.Cancelled {
					f.tx(func(ctx context.Context, tx pgx.Tx) error {
						_, err := f.store.CancelDirectoryV2Tx(ctx, tx, f.engine, f.p, ex.Task.ID, []string{f.id})
						return err
					})
				}
				if terminal == tasks.Failed {
					return tasks.Outcome{State: tasks.Failed, Code: "synthetic_failure"}
				}
				return staged
			}
			installDirectoryV2RuntimeKind(t, f, kind)
			task := submitDirectoryV2Runtime(f, "staged-user-assets")
			page, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
			if err != nil || page.Available {
				t.Fatal("queued task became data", err)
			}
			if _, err := f.engine.RunOne(context.Background(), "staged-user-owner"); err != nil {
				t.Fatal(err)
			}
			page, err = userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
			if err != nil || page.Available != (terminal == tasks.Succeeded) {
				t.Fatal("terminal eligibility incorrect", err)
			}
			if terminal != tasks.Succeeded {
				_, err = userAssetV2Detail(f, f.store, task.ID, "00000001-4455-6677-8899-aabbccddeeff")
				requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
			}
		})
	}
}

func TestUserAssetsV2CancelledTransactionReturnsNoPartialResponse(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grantDirectoryV2(f)
	o := userAssetsV2Observation()
	defer o.Discard()
	task := publishUserAssetsV2(f, o, "cancelled-read")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx, err := f.conn.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	out, err := f.store.UserAssetsV2ListTx(ctx, tx, f.p.TenantID, []string{f.id}, userAssetsV2Filter(f, task.ID, "", 1, 10))
	if err == nil || !reflect.DeepEqual(out, domains.UserAssetsV2List{}) {
		t.Fatal("cancelled transaction returned success")
	}
}

// No arbitrary age/freshness cutoff participates in stored evidence eligibility.
func TestUserAssetsV2OldObservationTimestampRemainsFactual(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	grantDirectoryV2(f)
	o := userAssetsV2Observation()
	defer o.Discard()
	o.Source.StartedAt = time.Date(2001, 1, 2, 3, 4, 5, 0, time.UTC)
	o.Source.CompletedAt = o.Source.StartedAt.Add(time.Second)
	task := publishUserAssetsV2(f, o, "old-timestamp")
	out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, task.ID, "", 1, 10))
	if err != nil || !out.Available || !out.Source.StartedAt.Equal(o.Source.StartedAt) {
		t.Fatal("observation age was mistaken for invalidity", err)
	}
}

func TestUserAssetsV2WrongDomainAndV1ObservationNeverSatisfyPinnedIdentity(t *testing.T) {
	f := directoryV2RuntimeFixture(t)
	o := directoryV2RuntimeObservation()
	defer o.Discard()
	legacy := ldapconnection.DirectoryObservation{Objects: []directoryassets.Object{o.Objects[0].Base}, Source: o.Source}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	digest := sha256.Sum256(raw)
	kind := f.store.DirectoryKind()
	kind.Execute = func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), domains.OpenDirectoryUseForTest(ctx, tx, ex.Task)
		})
		if err != nil {
			t.Fatal("legacy use open failed", err)
		}
		_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			_, err := tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$9)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.ActorID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt, raw, hex.EncodeToString(digest[:]))
			return 100, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err != nil {
			t.Fatal("legacy real SQL publication failed", err)
		}
		return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{}`)}
	}
	f.installKind(t, kind)
	var legacyTask tasks.Task
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.SubmitDirectoryTx(ctx, tx, f.engine, f.p, domains.DirectoryInput{DomainID: f.id, ExpectedRevision: "2", ExpectedCredentialGeneration: "2", IdempotencyKey: "v1-not-user-assets"})
		legacyTask = out.Task
		return err
	})
	if _, err := f.engine.RunOne(context.Background(), "legacy-asset-publisher"); err != nil {
		t.Fatal(err)
	}
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.DirectoryListTx(ctx, tx, f.p.TenantID, []string{f.id}, domains.DirectoryFilter{DomainID: f.id, ObservationID: legacyTask.ID, PageIdx: 1, PageSize: 25})
		if err != nil || !out.Available || out.Page.Total != 1 || out.List[0].GUID != legacy.Objects[0].GUID {
			t.Fatal("legacy fixture not real readable evidence", err)
		}
		return nil
	})
	out, err := userAssetsV2List(f, f.store, userAssetsV2Filter(f, "", "", 1, 10))
	if err != nil || out.Available || out.Page.Total != 0 {
		t.Fatal("v1-only data satisfied v2 read", err)
	}
	_, err = userAssetsV2List(f, f.store, userAssetsV2Filter(f, legacyTask.ID, "", 1, 10))
	requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
	_, err = userAssetV2Detail(f, f.store, legacyTask.ID, legacy.Objects[0].GUID)
	requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
	grantDirectoryV2(f)
	task := publishUserAssetsV2(f, o, "actual-v2-users")
	var otherID string
	f.tx(func(ctx context.Context, tx pgx.Tx) error {
		user, password := "synthetic@other.test", "Synthetic test-only pair"
		created, err := f.store.CreateTx(ctx, tx, f.p, domains.Input{Domain: "other.test", DCHostName: "dc1.other.test", LDAPAddr: "10.20.0.9", Port: "389", Username: &user, Password: &password, IdempotencyKey: "other-source"}, time.Now())
		otherID = created.DomainID
		return err
	})
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.UserAssetsV2ListTx(ctx, tx, f.p.TenantID, []string{f.id, otherID}, domains.UserAssetsV2Filter{DomainID: otherID, ExpectedRevision: "1", ExpectedCredentialRevision: "1", ObservationID: task.ID, PageIdx: 1, PageSize: 10})
		if !reflect.DeepEqual(out, domains.UserAssetsV2List{}) {
			t.Fatal("wrong domain disclosed the original source")
		}
		return err
	})
	requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
	err = f.attempt(func(ctx context.Context, tx pgx.Tx) error {
		out, err := f.store.UserAssetV2DetailTx(ctx, tx, f.p.TenantID, []string{f.id, otherID}, domains.UserAssetV2Input{DomainID: otherID, ExpectedRevision: "1", ExpectedCredentialRevision: "1", ObservationID: task.ID, ObjectGUID: o.Objects[0].Base.GUID})
		if !reflect.DeepEqual(out, domains.UserAssetV2Detail{}) {
			t.Fatal("GUID leaked through a wrong source triple")
		}
		return err
	})
	requireUserAssetsV2IntegrationError(t, err, 409, "directory_observation_unavailable")
}
