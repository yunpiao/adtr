package auth

// Marks only updates the closed permission vocabulary; it grants no role rights.
// Reduced access/resource fixtures use it without installing task-epoch machinery.
const SimulationDNSIPPermissionMarks = `
ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks','audit','audit_exports','system','schedules','task_archive','domains','operation_accounts','system_logs','directory_assets','user_asset_exports','group_asset_exports','computer_asset_exports','simulation_events','simulation_risk','simulation_detection','simulation_detection_schedules','simulation_labels','simulation_rule_catalog','simulation_rule_catalog_publish','simulation_detection_exceptions','simulation_dns_ip')); `

// Production migration installs the vocabulary and revocation serialization only.
// It never inserts grants; platform_admin is separately excluded in loadAccessGrants.
const SimulationDNSIPPermissionSchema = SimulationDNSIPPermissionMarks + `
CREATE FUNCTION adtr.simulation_dns_ip_grant_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ot text; nt text; orole text; nrole text;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP<>'INSERT' AND OLD.mark ='simulation_dns_ip' THEN ot:=OLD.tenant_id; orole:=OLD.role_id; END IF;
 IF TG_OP<>'DELETE' AND NEW.mark ='simulation_dns_ip' THEN nt:=NEW.tenant_id; nrole:=NEW.role_id; END IF;
 IF ot IS NOT NULL AND nt IS NOT NULL AND ot<>nt THEN
  PERFORM adtr.task_auth_lock(least(ot,nt)); PERFORM adtr.task_auth_lock(greatest(ot,nt));
 END IF;
 IF ot IS NOT NULL THEN PERFORM adtr.task_auth_bump(ot,ARRAY[orole]); END IF;
 IF nt IS NOT NULL AND (nt IS DISTINCT FROM ot OR nrole IS DISTINCT FROM orole) THEN PERFORM adtr.task_auth_bump(nt,ARRAY[nrole]); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER simulation_dns_ip_grant_epoch AFTER INSERT OR UPDATE OR DELETE ON adtr.access_permissions
 FOR EACH ROW EXECUTE FUNCTION adtr.simulation_dns_ip_grant_epoch();
`
