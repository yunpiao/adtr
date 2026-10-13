//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yunpiao/adtr/internal/directoryassets"
	"github.com/yunpiao/adtr/internal/domains"
	"github.com/yunpiao/adtr/internal/ldapconnection"
	"github.com/yunpiao/adtr/internal/tasks"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const groupMembersHTTPPath = "/api/group-assets/v4/members"

func mountGroupMembersV4HTTP(f *directoryV4HTTPFixture) {
	mux := http.NewServeMux()
	mux.Handle(groupMembersHTTPPath, f.service.GroupMembersV4Handler(f.store))
	mux.Handle(groupMembersHTTPPath+"/", f.service.GroupMembersV4Handler(f.store))
	mux.Handle("/", f.handler)
	f.handler = mux
}
func groupMembersV4HTTPPath(revision, pin, guid string) string {
	return groupMembersHTTPPath + "?" + userAssetsV3HTTPQuery(revision) + "&observationId=" + pin + "&objectGUID=" + guid
}

// Synthetic HTTP/SQL evidence only: actual Worker/TLS browser tests are separate.
func (f *directoryV4HTTPFixture) stageV4(observation ldapconnection.DirectoryV4Observation) *directoryHTTPSyntheticRun {
	f.t.Helper()
	objects := make([]json.RawMessage, 0, len(observation.Objects))
	for _, object := range observation.Objects {
		raw, err := directoryassets.EncodeStoredObjectV4(object)
		if err != nil {
			f.t.Fatal("validate synthetic object", err)
		}
		objects = append(objects, raw)
	}
	raw, err := json.Marshal(struct {
		DictionaryVersion int                            `json:"dictionaryVersion"`
		Objects           []json.RawMessage              `json:"objects"`
		Source            ldapconnection.DirectorySource `json:"source"`
	}{4, objects, observation.Source})
	for _, object := range objects {
		clear(object)
	}
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { clear(raw) })
	digest := sha256.Sum256(raw)
	staged, release, done := make(chan error, 1), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	run := &directoryHTTPSyntheticRun{release: func() { once.Do(func() { close(release) }) }, done: done}
	f.installV4(f.runtime, func(ctx context.Context, ex tasks.Execution) tasks.Outcome {
		version, err := ex.WithTx(ctx, ex.Task.ResultVersion, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
			tag, err := tx.Exec(ctx, `UPDATE adtr.domain_directory_task_uses SET state='opened',opened_at=clock_timestamp(),opener_owner=$4,opener_fencing_token=$5,opener_attempt=$6 WHERE tenant_id=$1 AND domain_id=$2 AND task_id=$3 AND state='reserved' AND purpose='domain.directory_read.v4' AND dictionary_version=4`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt)
			if err == nil && tag.RowsAffected() != 1 {
				err = fmt.Errorf("synthetic opened-use transition affected %d rows", tag.RowsAffected())
			}
			return 10, json.RawMessage(`{}`), json.RawMessage(`{}`), err
		})
		if err == nil {
			_, err = ex.WithTx(ctx, version, func(ctx context.Context, tx pgx.Tx) (int, json.RawMessage, json.RawMessage, error) {
				_, err := tx.Exec(ctx, `INSERT INTO adtr.domain_directory_observations(tenant_id,domain_id,task_id,actor_id,opener_owner,opener_fencing_token,opener_attempt,object_count,body,sha256,dictionary_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,4)`, ex.Task.TenantID, ex.Task.DomainID, ex.Task.ID, ex.Task.ActorID, ex.Task.LeaseOwner, ex.Task.FencingToken, ex.Task.Attempt, len(observation.Objects), raw, hex.EncodeToString(digest[:]))
				if err == nil {
					// This synthetic executor stages both outputs that the real LDAP
					// producer commits atomically. The audit view must still wait for
					// the real engine finish before exposing a successful result.
					var tag pgconn.CommandTag
					tag, err = tx.Exec(ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result)
 SELECT tenant_id,actor_id,domain_id,'domain_directory_v4_result',(payload->>'connectionRevision')::bigint,(payload->>'connectionRevision')::bigint,(payload->>'connectionCredentialGeneration')::bigint,task_id,'success'
 FROM adtr.tasks WHERE task_id=$1 AND kind='domain.directory_read.v4'`, ex.Task.ID)
					if err == nil && tag.RowsAffected() != 1 {
						err = fmt.Errorf("synthetic v4 audit stage did not match the admitted task")
					}
				}
				return 95, json.RawMessage(`{"policyRevision":"synthetic-v4-private-cursor"}`), json.RawMessage(`{"objectGUID":"synthetic-v4-private-result"}`), err
			})
		}
		staged <- err
		if err != nil {
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_stage_failed"}
		}
		select {
		case <-release:
			return tasks.Outcome{State: tasks.Succeeded, Result: json.RawMessage(`{"objectGUID":"synthetic-v4-private-result"}`)}
		case <-ctx.Done():
			<-release
			return tasks.Outcome{State: tasks.Failed, Code: "synthetic_cancelled"}
		}
	})
	ctx, cancel := context.WithCancel(f.ctx)
	engine := f.engine
	go func() {
		worked, err := engine.RunOne(ctx, "synthetic-directory-v4-http")
		if err == nil && !worked {
			err = fmt.Errorf("synthetic v4 executor found no admitted task")
		}
		done <- err
	}()
	f.t.Cleanup(func() {
		run.release()
		cancel()
		if !run.joined {
			select {
			case <-run.done:
			case <-time.After(10 * time.Second):
				f.t.Error("synthetic v4 executor did not return during cleanup")
			}
		}
	})
	select {
	case err := <-staged:
		if err != nil {
			f.t.Fatal("real guarded v4 publication", err)
		}
	case err := <-done:
		run.joined = true
		f.t.Fatal("real engine returned before v4 staging", err)
	case <-f.ctx.Done():
		f.t.Fatal("v4 staging timed out", f.ctx.Err())
	}
	return run
}

func (f *directoryV4HTTPFixture) finishV4(run *directoryHTTPSyntheticRun, task tasks.Task, state tasks.State) {
	f.t.Helper()
	run.release()
	select {
	case err := <-run.done:
		run.joined = true
		if err != nil {
			f.t.Fatal("real v4 engine finish/return acknowledgement", err)
		}
	case <-f.ctx.Done():
		f.t.Fatal("actual v4 executor return not observed", f.ctx.Err())
	}
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE task_id=$1 AND state=$2`, task.ID, string(state)) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE task_id=$1 AND purpose='domain.directory_read.v4' AND dictionary_version=4 AND state='quiesced' AND quiescence_reason='executor_returned'`, task.ID) != 1 || f.count(`SELECT count(*) FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v4' AND object_id=$1`, task.ID) != 0 {
		f.t.Fatal("actual v4 return did not persist the outcome and original-opener acknowledgement")
	}
}

func directoryV4HTTPObservation(users int) ldapconnection.DirectoryV4Observation {
	old := directoryV2HTTPObservation(users)
	out := ldapconnection.DirectoryV4Observation{Source: old.Source, Objects: []directoryassets.StoredObjectV4{}}
	for _, row := range old.Objects {
		identity := directoryassets.RawIdentityV3{}
		if row.Base.GUID == "00000000-0000-0000-0000-000000000001" {
			identity = directoryassets.RawIdentityV3{PrimaryGroupIDBytes: []byte("0"), ServicePrincipalNameBytes: [][]byte{[]byte("HOST/Z"), []byte("host/a\x00\n😀<script>"), []byte("HOST/a")}}
		}
		out.Objects = append(out.Objects, directoryassets.StoredObjectV4{Base: row.Base, Supplemental: row.Supplemental, Identity: identity})
	}
	return out
}

func groupMembersV4HTTPObservation() ldapconnection.DirectoryV4Observation {
	o := directoryV4HTTPObservation(4)
	for i := 0; i < 3; i++ {
		o.Objects[i].Base.Kind = directoryassets.Group
		o.Objects[i].Base.Classes = []string{"group", "top"}
	}
	o.Objects[1].Membership.MemberBytes = [][]byte{}
	o.Objects[2].Membership.MemberBytes = [][]byte{[]byte("CN=nested,DC=one,DC=invalid"), []byte("CN=foreign,DC=other,DC=invalid"), []byte("CN=raw\x00\n😀<script>")}
	return o
}
func TestGroupMembersV4MigratedHTTPCurrentAuthorityAndStates(t *testing.T) {
	f := newDirectoryV4HTTPFixture(t)
	mountGroupMembersV4HTTP(f)
	path := groupMembersV4HTTPPath("1", "unknown-observation", "00000000-0000-0000-0000-000000000001")
	f.request("", path, nil, 401)
	for _, actor := range []string{"member", "directory-http-no-assets", "directory-http-no-domains"} {
		f.request(actor, path, nil, 403)
	}
	f.request("directory-http-no-scope", path, nil, 404)
	f.request("directory-admin", strings.Replace(path, "domain-one", "domain-two", 1), nil, 404)
	f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	checks := operationalDecode[struct {
		Results []bool `json:"results"`
	}](t, f.request("directory-http-reader", "/api/access/check", map[string]any{"paths": []string{"GET " + groupMembersHTTPPath, "POST " + groupMembersHTTPPath, "GET " + groupMembersHTTPPath + "/", "POST /api/directory/v4/sync"}}, 200).Body.Bytes())
	if !reflect.DeepEqual(checks.Results, []bool{true, false, false, false}) {
		t.Fatal("introspection authority mismatch", checks.Results)
	}
	f.bind()
	f.grantV4("directory-grant", "platform_admin", "member-v4-grant")
	task := f.submitV4("directory-submit-one", "member-v4-observe")
	o := groupMembersV4HTTPObservation()
	defer o.Discard()
	run := f.stageV4(o)
	mountGroupMembersV4HTTP(f)
	path = groupMembersV4HTTPPath("2", task.ID, o.Objects[0].Base.GUID)
	f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	f.finishV4(run, task, tasks.Succeeded)
	for i := 0; i < 3; i++ {
		path = groupMembersV4HTTPPath("2", task.ID, o.Objects[i].Base.GUID)
		w := f.request("directory-http-reader", path, nil, 200)
		assertUserAssetsV3HTTPHeaders(t, w, f.clients["directory-http-reader"].id)
		out := operationalDecode[domains.GroupMembersV4](t, w.Body.Bytes())
		want, err := directoryassets.ProjectV4(o.Objects[i])
		if err != nil || out.DictionaryVersion != 4 || out.ObservationID != task.ID || !reflect.DeepEqual(out.Object, want) {
			t.Fatal("raw membership state/facts mismatch", err)
		}
	}
	f.request("directory-http-reader", groupMembersV4HTTPPath("2", task.ID, o.Objects[3].Base.GUID), nil, 404)
	f.error(f.request("directory-http-reader", groupMembersV4HTTPPath("1", task.ID, o.Objects[0].Base.GUID), nil, 409), "selection_changed")
	f.error(f.request("directory-http-reader", groupMembersV4HTTPPath("2", "missing", o.Objects[0].Base.GUID), nil, 409), "directory_observation_unavailable")
	f.installV4(f.runtimeWithGates(false, false), nil)
	mountGroupMembersV4HTTP(f)
	f.request("directory-http-reader", path, nil, 200)
	// No read can reserve credentials or submit additional producer tasks.
	if f.count(`SELECT count(*) FROM adtr.tasks WHERE kind='domain.directory_read.v4'`) != 1 || f.count(`SELECT count(*) FROM adtr.domain_directory_task_uses WHERE purpose='domain.directory_read.v4'`) != 1 {
		t.Fatal("read mutated producer state")
	}
}
func TestGroupMembersV4MigratedHTTPRevocation(t *testing.T) {
	for _, tc := range []struct {
		name, sql, code string
		args            []any
		status          int
	}{
		{"disabled", `UPDATE adtr.users SET disabled=true WHERE username='directory-http-reader'`, "unauthenticated", nil, 401},
		{"session", `DELETE FROM adtr.sessions WHERE user_id=(SELECT id FROM adtr.users WHERE username='directory-http-reader')`, "unauthenticated", nil, 401},
		{"scope", `DELETE FROM adtr.resource_role_groups WHERE tenant_id=$1 AND role_id=$2`, "not_found", []any{governanceHTTPTenant, directoryHTTPReaderRole}, 404},
		{"expired", `UPDATE adtr.resource_tenant_config SET expire_time=extract(epoch FROM clock_timestamp()-interval '1 hour')::bigint WHERE tenant_id=$1`, "tenant_expired", []any{governanceHTTPTenant}, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDirectoryV4HTTPFixture(t)
			f.bind()
			f.grantV4("directory-grant", "platform_admin", "revoke-v4-grant")
			task := f.submitV4("directory-submit-one", "revoke-v4-observe")
			o := groupMembersV4HTTPObservation()
			defer o.Discard()
			f.finishV4(f.stageV4(o), task, tasks.Succeeded)
			mountGroupMembersV4HTTP(f)
			path := groupMembersV4HTTPPath("2", task.ID, o.Objects[0].Base.GUID)
			f.request("directory-http-reader", path, nil, 200)
			f.exec(tc.sql, tc.args...)
			f.error(f.request("directory-http-reader", path, nil, tc.status), tc.code)
		})
	}
}
func TestGroupMembersV4HTTPWholeObservationBeforeSelectedGroup(t *testing.T) {
	f := newDirectoryV4HTTPFixture(t)
	f.bind()
	f.grantV4("directory-grant", "platform_admin", "corrupt-v4-grant")
	task := f.submitV4("directory-submit-one", "corrupt-v4-observe")
	o := groupMembersV4HTTPObservation()
	defer o.Discard()
	f.finishV4(f.stageV4(o), task, tasks.Succeeded)
	mountGroupMembersV4HTTP(f)
	path := groupMembersV4HTTPPath("2", task.ID, o.Objects[2].Base.GUID)
	f.request("directory-http-reader", path, nil, 200)
	var raw []byte
	if err := f.conn.QueryRow(f.ctx, `SELECT body FROM adtr.domain_directory_observations WHERE task_id=$1`, task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	// Corrupt the last, nonselected USER membership and recompute digest. This
	// proves full validation precedes group/GUID selection, not hash alone.
	needle := []byte(`"membership":{"memberBytes":null}`)
	index := bytes.LastIndex(raw, needle)
	if index < 0 {
		t.Fatal("missing corruption fixture")
	}
	raw = append(append(append([]byte{}, raw[:index]...), []byte(`"membership":{"memberBytes":[]}`)...), raw[index+len(needle):]...)
	sum := sha256.Sum256(raw)
	// Owned disposable database corruption case only; never a production action.
	f.exec(`ALTER TABLE adtr.domain_directory_observations DISABLE TRIGGER domain_directory_observations_immutable`)
	f.exec(`UPDATE adtr.domain_directory_observations SET body=$2,sha256=$3 WHERE task_id=$1`, task.ID, raw, hex.EncodeToString(sum[:]))
	f.exec(`ALTER TABLE adtr.domain_directory_observations ENABLE TRIGGER domain_directory_observations_immutable`)
	f.error(f.request("directory-http-reader", path, nil, 409), "directory_observation_unavailable")
	f.error(f.request("directory-http-reader", "/api/directory/v4/observation?domainId=domain-one&observationId="+task.ID+"&kind=group", nil, 409), "directory_observation_unavailable")
}
