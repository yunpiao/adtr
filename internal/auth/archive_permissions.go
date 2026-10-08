package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"github.com/yunpiao/adtr/internal/tasks"
)

const ArchivePermissionMarks = `
ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks','audit','audit_exports','system','schedules','task_archive'));
`

// New archive grants are explicit; existing custom roles acquire no permission.
// Revocation/regrant bumps the same server-owned epoch as other task controls.
const ArchivePermissionSchema = ArchivePermissionMarks + `
CREATE FUNCTION adtr.archive_grant_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ot text; nt text; orole text; nrole text;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP<>'INSERT' AND OLD.mark='task_archive' THEN ot:=OLD.tenant_id; orole:=OLD.role_id; END IF;
 IF TG_OP<>'DELETE' AND NEW.mark='task_archive' THEN nt:=NEW.tenant_id; nrole:=NEW.role_id; END IF;
 IF ot IS NOT NULL AND nt IS NOT NULL AND ot<>nt THEN
  PERFORM adtr.task_auth_lock(least(ot,nt)); PERFORM adtr.task_auth_lock(greatest(ot,nt));
 END IF;
 IF ot IS NOT NULL THEN PERFORM adtr.task_auth_bump(ot,ARRAY[orole]); END IF;
 IF nt IS NOT NULL AND (nt IS DISTINCT FROM ot OR nrole IS DISTINCT FROM orole) THEN PERFORM adtr.task_auth_bump(nt,ARRAY[nrole]); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
CREATE TRIGGER archive_grant_epoch AFTER INSERT OR UPDATE OR DELETE ON adtr.access_permissions FOR EACH ROW EXECUTE FUNCTION adtr.archive_grant_epoch();
`

func NewArchiveAuthorizer() tasks.Authorizer { return archiveAuthorizer(time.Now) }
func archiveAuthorizer(now func() time.Time) tasks.Authorizer {
	base := taskAuthorizer(now)
	return func(ctx context.Context, tx pgx.Tx, p tasks.Principal, scope tasks.Scope, action tasks.Action) (string, error) {
		if scope.TaskName != taskarchive.HealthKind || scope.DomainID != "platform" || !scope.Platform || action != tasks.Read && action != tasks.Write {
			return "", tasks.ErrAuthorization
		}
		epoch, err := base(ctx, tx, p, scope, action)
		if err != nil {
			return "", err
		}
		var role string
		if err = tx.QueryRow(ctx, `SELECT COALESCE(NULLIF(role_id,''),role) FROM adtr.users WHERE tenant_id=$1 AND id=$2`, p.TenantID, p.ActorID).Scan(&role); err != nil {
			return "", err
		}
		if role == "platform_admin" {
			return epoch, nil
		}
		var read, write bool
		err = tx.QueryRow(ctx, `SELECT readable,writeable FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2 AND mark='task_archive'`, p.TenantID, role).Scan(&read, &write)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", tasks.ErrAuthorization
		}
		if err != nil {
			return "", err
		}
		if !read || action == tasks.Write && !write {
			return "", tasks.ErrAuthorization
		}
		return epoch, nil
	}
}
