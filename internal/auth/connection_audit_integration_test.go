//go:build integration

package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestDomainControlAuditUsesLiveScopeAndProtectedProjection(t *testing.T) {
	f := newAuditFixture(t)
	admin := f.client(f.admin)
	var id int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result) VALUES('one',$1,'domain-a','domain_create',0,1,1,'saved') RETURNING id`, f.admin).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rows := auditListRows(t, f.callAudit(admin, "?filterEvent=domain_create", nil, 200)); len(rows) != 0 {
		t.Fatal("admin bypassed explicit domain scope")
	}
	f.seedGroup("one", "domain-audit-scope", "platform_admin", "domain-a")
	rows := auditListRows(t, f.callAudit(admin, "?filterEvent=domain_create", nil, 200))
	if len(rows) != 1 {
		t.Fatal("domain audit producer missing")
	}
	row := rows[0].(map[string]any)
	if row["ID"] != fmt.Sprintf("domain.%d", id) || row["deletable"] != false || row["eventResult"] != "SUCCESS" || row["loginUser"] != nil {
		t.Fatal("domain control projection incorrect", row)
	}
	f.callAudit(admin, "/delete", f.proof(map[string]any{"id": []string{fmt.Sprintf("domain.%d", id)}, "reason": "synthetic test"}), 409)
	var count int
	if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.audit_visibility WHERE source='domain'").Scan(&count); err != nil || count != 0 {
		t.Fatal("protected domain history hidden", err)
	}
	f.exec(`INSERT INTO adtr.tasks(task_id,tenant_id,domain_id,kind,payload_version,payload,payload_hash,actor_id,authorization_version,idempotency_key,state,max_attempts) VALUES('staged-domain-observation','one','domain-a','domain.connection_test',1,'{}','synthetic',$1,$2,'staged-domain-observation','queued',1)`, f.admin, fmt.Sprint(f.version(f.admin)))
	f.exec(`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,task_id,result) VALUES('one',$1,'domain-a','domain_test_result',1,1,1,'staged-domain-observation','success')`, f.admin)
	rows = auditListRows(t, f.callAudit(admin, "?filterEvent=domain_test_result", nil, 200))
	if len(rows) != 1 || rows[0].(map[string]any)["eventResult"] != "NONE" {
		t.Fatal("staged observation advertised final success", rows)
	}
	f.exec("UPDATE adtr.tasks SET state='succeeded' WHERE task_id='staged-domain-observation'")
	rows = auditListRows(t, f.callAudit(admin, "?filterEvent=domain_test_result", nil, 200))
	if len(rows) != 1 || rows[0].(map[string]any)["eventResult"] != "SUCCESS" {
		t.Fatal("terminal diagnostic success absent", rows)
	}
}

func TestDomainProducerExportExpiresWithoutEpochMutation(t *testing.T) {
	f := newAuditFixture(t)
	c := f.client(f.admin)
	f.seedGroup("one", "domain-producer-export", "platform_admin", "domain-a")
	expires := time.Now().Unix() + 5
	f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", expires)
	epoch := f.version(f.admin)
	f.exec(`INSERT INTO adtr.domain_audit(tenant_id,actor_id,domain_id,action,old_revision,new_revision,credential_revision,result,occurred_at) VALUES('one',$1,'domain-a','domain_create',0,1,1,'saved','2020-02-01T12:00:00Z')`, f.admin)
	body := exportBody("domain-producer-expiry", "event")
	body["filterEvent"] = []string{"domain_create"}
	id := f.taskID(f.callAudit(c, "/exports", f.proof(body), 200))
	f.runExport()
	out := f.callAudit(c, "/exports/detail?taskUUID="+id, nil, 200)
	if out["rowCount"] != float64(1) || out["downloadReady"] != true {
		t.Fatal("domain-only export unavailable before expiry", out)
	}
	w := f.auditResponse(c, "/exports/download?taskUUID="+id, nil)
	if w.Code != 200 || w.Body.Len() == 0 {
		t.Fatal("domain-only export download failed before expiry", w.Code)
	}
	time.Sleep(max(0, time.Until(time.Unix(expires, 0).Add(50*time.Millisecond))))
	if f.version(f.admin) != epoch {
		t.Fatal("fixture changed epoch instead of time")
	}
	f.callAudit(c, "/exports/detail?taskUUID="+id, nil, 403)
	f.callAudit(c, "/exports/download?taskUUID="+id, nil, 403)
	if rows := auditListRows(t, f.callAudit(c, "?filterEvent=domain_create", nil, 200)); len(rows) != 0 {
		t.Fatal("expired source rows disclosed")
	}
}
