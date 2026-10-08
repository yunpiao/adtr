//go:build integration

package auth

import (
	"fmt"
	"testing"
)

func TestMaintenanceControlAuditCannotBeHidden(t *testing.T) {
	f := newAuditFixture(t)
	admin := f.client(f.admin)
	for _, action := range []string{"schedule.create", "schedule.enable", "schedule.pause", "task.archive", "task.restore"} {
		var id int64
		if err := f.conn.QueryRow(f.ctx, "INSERT INTO adtr.auth_audit(tenant_id,actor_id,action,target_id) VALUES('one',$1,$2,$1) RETURNING id", f.admin, action+":synthetic-operation").Scan(&id); err != nil {
			t.Fatal(err)
		}
		out := f.callAudit(admin, "?filterEvent="+action, nil, 200)
		list := out["List"].([]any)
		if len(list) != 1 {
			t.Fatalf("control event not normalized: %s", action)
		}
		row := list[0].(map[string]any)
		if row["deletable"] != false || row["eventResult"] != "SUCCESS" {
			t.Fatalf("control evidence not protected: %#v", row)
		}
		f.callAudit(admin, "/delete", f.proof(map[string]any{"id": []string{fmt.Sprintf("auth.%d", id)}, "reason": "synthetic protection check"}), 409)
		var count int
		if err := f.conn.QueryRow(f.ctx, "SELECT count(*) FROM adtr.audit_visibility WHERE tenant_id='one' AND source='auth' AND source_id=$1 AND hidden", id).Scan(&count); err != nil || count != 0 {
			t.Fatal("control visibility changed", err)
		}
	}
}
