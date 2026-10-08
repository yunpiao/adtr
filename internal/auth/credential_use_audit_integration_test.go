//go:build integration

package auth

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/tasks"
)

func TestCredentialUseControlAuditScopeAndRealArtifact(t *testing.T) {
	f := newCredentialUseFixture(t)
	account := f.createAccount("credential-control-account")
	admin := f.client(f.admin)
	// A function-authorized reader without this domain must see no control row.
	f.exec("INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES('one','aaaaaaaaaaaaaaaaaaaaaaaa','Audit only')")
	f.exec("INSERT INTO adtr.access_permissions(tenant_id,role_id,mark,readable,writeable) VALUES('one','aaaaaaaaaaaaaaaaaaaaaaaa','audit',true,false)")
	var readerID int64
	if err := f.conn.QueryRow(f.ctx, `INSERT INTO adtr.users(tenant_id,username,password_hash,role,role_id,must_change,password_updated_at)
 SELECT 'one','audit-only-user',password_hash,'viewer','aaaaaaaaaaaaaaaaaaaaaaaa',false,password_updated_at FROM adtr.users WHERE id=$1 RETURNING id`, f.admin).Scan(&readerID); err != nil {
		t.Fatal(err)
	}
	reader := f.client(readerID)
	result := f.callUse(admin, "/grant", f.proof(useMutationBody(account, narrowResourceRole, "0", "credential-control-grant")), 200)
	engine, err := tasks.New(f.s.database, tasks.ProductionRegistry(audit.Kind(), f.store.Kind()), taskAuthorizer(f.s.now), schemaversion.Current)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = engine
	a := &auditFixture{f.taskFixture}
	if rows := auditListRows(t, a.callAudit(reader, "?filterEvent=credential_use_grant", nil, 200)); len(rows) != 0 {
		t.Fatal("function-only reader saw domain control")
	}
	rows := auditListRows(t, a.callAudit(admin, "?filterEvent=credential_use_grant", nil, 200))
	if len(rows) != 1 {
		t.Fatal("missing credential control", rows)
	}
	row := rows[0].(map[string]any)
	var eventID int64
	if err = f.conn.QueryRow(f.ctx, "SELECT id FROM adtr.operation_account_use_audit WHERE account_id=$1 AND action='credential_use_grant'", account).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	if row["ID"] != fmt.Sprintf("credential_use.%d", eventID) || row["deletable"] != false || row["eventResult"] != "SUCCESS" {
		t.Fatal("unsafe control projection", row)
	}
	raw, _ := json.Marshal(row)
	for _, value := range []string{"Synthetic Secret", "registry-reader@alternate.test", resourceTestPassword, resourceTestSecret, "ciphertext", "actorPassword", "totpCode"} {
		if bytes.Contains(raw, []byte(value)) {
			t.Fatal("secret material in control projection")
		}
	}
	if !bytes.Contains(raw, []byte(account)) || !bytes.Contains(raw, []byte(result["grantRevision"].(string))) {
		t.Fatal("missing actual safe grant identity")
	}
	a.callAudit(admin, "/delete", f.proof(map[string]any{"id": []string{fmt.Sprintf("credential_use.%d", eventID)}, "reason": "synthetic protected control"}), 409)
	if f.scalar("SELECT count(*) FROM adtr.audit_visibility WHERE source='credential_use'") != 0 {
		t.Fatal("credential control was hidden")
	}
	body := map[string]any{"filterEvent": []string{"credential_use_grant"}, "selectColumn": []string{"eventArgs"}, "idempotencyKey": "credential-control-export"}
	task := a.taskID(a.callAudit(admin, "/exports", f.proof(body), 200))
	a.runExport()
	detail := a.callAudit(admin, "/exports/detail?taskUUID="+task, nil, 200)
	if detail["rowCount"] != float64(1) || detail["downloadReady"] != true {
		t.Fatal("control artifact unavailable", detail)
	}
	var provenance string
	if err = f.conn.QueryRow(f.ctx, "SELECT domain_id FROM adtr.audit_export_rows WHERE task_id=$1 AND source='credential_use'", task).Scan(&provenance); err != nil || provenance != f.domain {
		t.Fatal("artifact lost exact domain provenance", err)
	}
	w := a.auditResponse(admin, "/exports/download?taskUUID="+task, nil)
	if w.Code != 200 {
		t.Fatal("control download failed", w.Code)
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := false
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
		if file.Name == "xl/worksheets/sheet1.xml" && bytes.Contains(data, []byte(account)) && bytes.Contains(data, []byte(f.domain)) {
			found = true
		}
		for _, secret := range []string{"Synthetic Secret", "registry-reader@alternate.test", resourceTestPassword, resourceTestSecret, "ciphertext"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("secret in control workbook")
			}
		}
	}
	if !found {
		t.Fatal("actual safe control values absent from worksheet")
	}
	if f.scalar("SELECT count(*) FROM adtr.tasks WHERE kind<>'audit.export'") != 0 || f.scalar("SELECT count(*) FROM adtr.operation_account_dependencies") != 0 {
		t.Fatal("governance admitted credential execution")
	}
}
