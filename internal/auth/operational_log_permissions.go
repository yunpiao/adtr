package auth

// A new closed permission does not grant it to any existing custom role. Its
// changes participate in the same durable epoch fencing as the task consumer.
const OperationalLogPermissionMarks = `
ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks','audit','audit_exports','system','schedules','task_archive','domains','operation_accounts','system_logs'));
`
const OperationalLogPermissionSchema = OperationalLogPermissionMarks + `
CREATE FUNCTION adtr.operational_log_grant_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_t text;new_t text;old_r text;new_r text;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW;END IF;
 IF TG_OP<>'INSERT' AND OLD.mark IN ('system','system_logs') THEN old_t:=OLD.tenant_id;old_r:=OLD.role_id;END IF;
 IF TG_OP<>'DELETE' AND NEW.mark IN ('system','system_logs') THEN new_t:=NEW.tenant_id;new_r:=NEW.role_id;END IF;
 IF old_t IS NOT NULL AND new_t IS NOT NULL AND old_t<>new_t THEN PERFORM adtr.task_auth_lock(least(old_t,new_t));PERFORM adtr.task_auth_lock(greatest(old_t,new_t));END IF;
 IF old_t IS NOT NULL THEN PERFORM adtr.task_auth_bump(old_t,ARRAY[old_r]);END IF;
 IF new_t IS NOT NULL AND (new_t IS DISTINCT FROM old_t OR new_r IS DISTINCT FROM old_r) THEN PERFORM adtr.task_auth_bump(new_t,ARRAY[new_r]);END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
END;$$;
CREATE TRIGGER operational_log_grant_epoch AFTER INSERT OR UPDATE OR DELETE ON adtr.access_permissions FOR EACH ROW EXECUTE FUNCTION adtr.operational_log_grant_epoch();
`
