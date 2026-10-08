package auth

const SchedulePermissionMarks = `
ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks','audit','audit_exports','system','schedules','task_archive'));
`

// SchedulePermissionSchema never adds grants to custom roles. Every effective
// schedule grant mutation advances its users' existing monotonic actor epochs.
const SchedulePermissionSchema = SchedulePermissionMarks + `
CREATE FUNCTION adtr.schedule_grant_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ot text; nt text; orole text; nrole text;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP<>'INSERT' AND OLD.mark='schedules' THEN ot:=OLD.tenant_id; orole:=OLD.role_id; END IF;
 IF TG_OP<>'DELETE' AND NEW.mark='schedules' THEN nt:=NEW.tenant_id; nrole:=NEW.role_id; END IF;
 IF ot IS NOT NULL AND nt IS NOT NULL AND ot<>nt THEN
  PERFORM adtr.task_auth_lock(least(ot,nt));
  PERFORM adtr.task_auth_lock(greatest(ot,nt));
 END IF;
 IF ot IS NOT NULL THEN PERFORM adtr.task_auth_bump(ot,ARRAY[orole]); END IF;
 IF nt IS NOT NULL AND (nt IS DISTINCT FROM ot OR nrole IS DISTINCT FROM orole) THEN PERFORM adtr.task_auth_bump(nt,ARRAY[nrole]); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
CREATE TRIGGER schedule_grant_epoch AFTER INSERT OR UPDATE OR DELETE ON adtr.access_permissions
 FOR EACH ROW EXECUTE FUNCTION adtr.schedule_grant_epoch();
`
