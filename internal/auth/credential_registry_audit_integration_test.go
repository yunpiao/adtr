//go:build integration

package auth

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
	"io"
	"strings"
	"testing"
	"time"
)

func TestOperationAccountAuditScopeProtectionAndExportExpiry(t *testing.T) {
	f := newOperationAccountFixture(t)
	accountID := f.createAccount("audit-account-create")
	engine, err := tasks.New(f.s.database, tasks.ProductionRegistry(audit.Kind(), f.store.Kind()), taskAuthorizer(f.s.now), schemaversion.Current)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = engine
	a := &auditFixture{f.taskFixture}
	c := f.client(f.admin)
	var eventID int64
	if err = f.conn.QueryRow(f.ctx, "SELECT id FROM adtr.operation_account_audit WHERE tenant_id='one' AND account_id=$1 AND action='operation_account_create'", accountID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	// Builtin administration cannot replace an explicit current domain grant.
	f.exec("DELETE FROM adtr.resource_role_groups WHERE tenant_id='one' AND role_id='platform_admin'")
	if rows := auditListRows(t, a.callAudit(c, "?filterEvent=operation_account_create", nil, 200)); len(rows) != 0 {
		t.Fatal("ungranted account audit disclosed")
	}
	f.exec("INSERT INTO adtr.resource_role_groups(tenant_id,group_id,role_id) VALUES('one',$1,'platform_admin')", "grant-"+f.domain)
	rows := auditListRows(t, a.callAudit(c, "?filterEvent=operation_account_create", nil, 200))
	if len(rows) != 1 {
		t.Fatal("operation audit producer missing")
	}
	row := rows[0].(map[string]any)
	if row["ID"] != fmt.Sprintf("operation_account.%d", eventID) || row["deletable"] != false || row["eventResult"] != "SUCCESS" {
		t.Fatal("operation control projection invalid", row)
	}
	raw, _ := json.Marshal(row)
	for _, secret := range []string{"registry-reader@alternate.test", "Synthetic Secret", "Registry label"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("input material exposed in audit")
		}
	}
	a.callAudit(c, "/delete", f.proof(map[string]any{"id": []string{fmt.Sprintf("operation_account.%d", eventID)}, "reason": "synthetic protection test"}), 409)
	var hidden int
	if err = f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.audit_visibility WHERE source='operation_account'").Scan(&hidden); err != nil || hidden != 0 {
		t.Fatal("protected account control hidden", err)
	}
	expires := time.Now().Unix() + 5
	f.exec("UPDATE adtr.resource_tenant_config SET expire_time=$1 WHERE tenant_id='one'", expires)
	epoch := f.version(f.admin)
	body := map[string]any{"filterEvent": []string{"operation_account_create"}, "selectColumn": []string{"eventArgs"}, "idempotencyKey": "account-producer-expiry"}
	id := a.taskID(a.callAudit(c, "/exports", f.proof(body), 200))
	a.runExport()
	detail := a.callAudit(c, "/exports/detail?taskUUID="+id, nil, 200)
	if detail["rowCount"] != float64(1) || detail["downloadReady"] != true {
		t.Fatal("account-only export not ready", detail)
	}
	w := a.auditResponse(c, "/exports/download?taskUUID="+id, nil)
	if w.Code != 200 {
		t.Fatal("account-only download failed", w.Code)
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	projectedRow := false
	for _, file := range archive.File {
		r, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(io.LimitReader(r, 1<<20))
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if file.Name == "xl/worksheets/sheet1.xml" && bytes.Contains(data, []byte(accountID)) && bytes.Contains(data, []byte(f.domain)) {
			projectedRow = true
		}
		for _, secret := range []string{"registry-reader@alternate.test", "Synthetic Secret", "Registry label"} {
			if strings.Contains(string(data), secret) {
				t.Fatal("input material exposed in XLSX")
			}
		}
	}
	if !projectedRow {
		t.Fatal("worksheet missing the actual safe account/domain projection")
	}
	time.Sleep(max(0, time.Until(time.Unix(expires, 0).Add(50*time.Millisecond))))
	if f.version(f.admin) != epoch {
		t.Fatal("test mutated epoch instead of time")
	}
	a.callAudit(c, "/exports/detail?taskUUID="+id, nil, 403)
	a.callAudit(c, "/exports/download?taskUUID="+id, nil, 403)
	if rows := auditListRows(t, a.callAudit(c, "?filterEvent=operation_account_create", nil, 200)); len(rows) != 0 {
		t.Fatal("expired account audit disclosed")
	}
	types := a.callAudit(c, "/types", nil, 200)
	raw, _ = json.Marshal(types)
	if bytes.Contains(raw, []byte("operation_account_create")) {
		t.Fatal("expired source leaked through type dictionary")
	}
}
