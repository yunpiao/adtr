package auth

// TaskAuthorizationSchema follows ResourceSchema in migration 5. Version values
// are server-owned, monotonic epochs, never permission hashes or session tokens.
// All existing application mutations already hold tenant before actor locks.
const TaskAuthorizationSchema = `
CREATE SEQUENCE adtr.task_authorization_epoch AS bigint NO CYCLE;
ALTER TABLE adtr.users ADD COLUMN authorization_version bigint NOT NULL DEFAULT 0;
UPDATE adtr.users SET authorization_version=nextval('adtr.task_authorization_epoch');
ALTER TABLE adtr.users ADD CONSTRAINT users_authorization_version_positive CHECK(authorization_version>0);

-- A row trigger can run after PostgreSQL has locked the modified row. Never wait
-- here for a tenant lock: abort nonconforming concurrent SQL instead of forming
-- a row->tenant / tenant->row deadlock. The normal API already owns this lock.
CREATE FUNCTION adtr.task_auth_lock(tenant text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('adtr-access:' || tenant,0)) THEN
  RAISE EXCEPTION 'authorization mutation must acquire tenant lock before rows' USING ERRCODE='40001';
 END IF;
END;
$$;

CREATE FUNCTION adtr.task_auth_user_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.authorization_version<>0 THEN RAISE EXCEPTION 'authorization version is server owned' USING ERRCODE='42501'; END IF;
  NEW.authorization_version := nextval('adtr.task_authorization_epoch');
  RETURN NEW;
 END IF;
 IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id THEN
  RAISE EXCEPTION 'actor identity is immutable' USING ERRCODE='42501';
 END IF;
 IF NEW.authorization_version IS DISTINCT FROM OLD.authorization_version THEN
  IF pg_trigger_depth()<=1 OR NEW.authorization_version<=OLD.authorization_version THEN
   RAISE EXCEPTION 'authorization version is server owned' USING ERRCODE='42501';
  END IF;
  PERFORM adtr.task_auth_lock(OLD.tenant_id);
 END IF;
 IF ROW(NEW.password_hash,NEW.must_change,NEW.disabled,NEW.password_updated_at,NEW.mfa_secret,NEW.role,NEW.role_id)
 IS DISTINCT FROM ROW(OLD.password_hash,OLD.must_change,OLD.disabled,OLD.password_updated_at,OLD.mfa_secret,OLD.role,OLD.role_id) THEN
  PERFORM adtr.task_auth_lock(OLD.tenant_id);
  NEW.authorization_version := nextval('adtr.task_authorization_epoch');
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER task_auth_user_revision BEFORE INSERT OR UPDATE ON adtr.users
 FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_user_revision();

-- NULL roles means every tenant actor. Lock all matching actors in ID order,
-- then update the already locked rows. Repeated changes can consume several
-- epochs; transaction rollback leaves each actor's stored epoch unchanged.
CREATE FUNCTION adtr.task_auth_bump(tenant text,roles text[]) RETURNS void LANGUAGE plpgsql AS $$
DECLARE actor bigint;
BEGIN
 PERFORM adtr.task_auth_lock(tenant);
 FOR actor IN SELECT id FROM adtr.users WHERE tenant_id=tenant
  AND (roles IS NULL OR COALESCE(NULLIF(role_id,''),role)=ANY(roles)) ORDER BY id FOR UPDATE
 LOOP
  UPDATE adtr.users SET authorization_version=nextval('adtr.task_authorization_epoch') WHERE id=actor;
 END LOOP;
END;
$$;

CREATE FUNCTION adtr.task_auth_grant_revision() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_tenant text; new_tenant text; old_roles text[]; new_roles text[];
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP<>'INSERT' THEN old_tenant:=OLD.tenant_id; END IF;
 IF TG_OP<>'DELETE' THEN new_tenant:=NEW.tenant_id; END IF;
 -- Mutations that only affect descriptive metadata do not revoke work.
 IF TG_TABLE_NAME='access_permissions' THEN
  IF TG_OP<>'INSERT' AND OLD.mark='tasks' THEN old_roles:=ARRAY[OLD.role_id]; ELSE old_tenant:=NULL; END IF;
  IF TG_OP<>'DELETE' AND NEW.mark='tasks' THEN new_roles:=ARRAY[NEW.role_id]; ELSE new_tenant:=NULL; END IF;
 ELSIF TG_TABLE_NAME='resource_role_groups' THEN
  IF old_tenant IS NOT NULL THEN old_roles:=ARRAY[OLD.role_id]; END IF;
  IF new_tenant IS NOT NULL THEN new_roles:=ARRAY[NEW.role_id]; END IF;
 ELSIF TG_TABLE_NAME='resource_group_members' THEN
  IF old_tenant IS NOT NULL THEN SELECT COALESCE(array_agg(role_id),'{}'::text[]) INTO old_roles FROM adtr.resource_role_groups WHERE tenant_id=old_tenant AND group_id=OLD.group_id; END IF;
  IF new_tenant IS NOT NULL THEN SELECT COALESCE(array_agg(role_id),'{}'::text[]) INTO new_roles FROM adtr.resource_role_groups WHERE tenant_id=new_tenant AND group_id=NEW.group_id; END IF;
 ELSIF TG_TABLE_NAME='resource_groups' THEN
  SELECT COALESCE(array_agg(role_id),'{}'::text[]) INTO old_roles FROM adtr.resource_role_groups WHERE tenant_id=old_tenant AND group_id=OLD.id;
  new_tenant:=NULL;
 ELSIF TG_TABLE_NAME='resource_domains' THEN
  IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.id,NEW.active) IS NOT DISTINCT FROM ROW(OLD.tenant_id,OLD.id,OLD.active) THEN RETURN NEW; END IF;
 ELSIF TG_TABLE_NAME='resource_tenant_config' THEN
  IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.max_ad_count,NEW.expire_time) IS NOT DISTINCT FROM ROW(OLD.tenant_id,OLD.max_ad_count,OLD.expire_time) THEN RETURN NEW; END IF;
 ELSE RAISE EXCEPTION 'unsupported authorization trigger';
 END IF;
 -- Only cross-tenant direct SQL could need two locks; try-locking in lexical
 -- order cannot wait on a lower lock while retaining a higher one.
 IF old_tenant IS NOT NULL AND new_tenant IS NOT NULL AND old_tenant<>new_tenant THEN
  PERFORM adtr.task_auth_lock(least(old_tenant,new_tenant));
  PERFORM adtr.task_auth_lock(greatest(old_tenant,new_tenant));
 END IF;
 IF old_tenant IS NOT NULL THEN PERFORM adtr.task_auth_bump(old_tenant,old_roles); END IF;
 IF new_tenant IS NOT NULL THEN PERFORM adtr.task_auth_bump(new_tenant,new_roles); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
CREATE TRIGGER task_auth_permissions BEFORE INSERT OR UPDATE OR DELETE ON adtr.access_permissions FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_grant_revision();
CREATE TRIGGER task_auth_role_groups BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_role_groups FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_grant_revision();
CREATE TRIGGER task_auth_group_members BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_group_members FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_grant_revision();
CREATE TRIGGER task_auth_groups BEFORE DELETE ON adtr.resource_groups FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_grant_revision();
CREATE TRIGGER task_auth_domains BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_domains FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_grant_revision();
CREATE TRIGGER task_auth_tenant BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_tenant_config FOR EACH ROW EXECUTE FUNCTION adtr.task_auth_grant_revision();

-- TRUNCATE bypasses row triggers and is not an authorization mutation API.
CREATE TRIGGER task_auth_users_no_truncate BEFORE TRUNCATE ON adtr.users FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_permissions_no_truncate BEFORE TRUNCATE ON adtr.access_permissions FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_roles_no_truncate BEFORE TRUNCATE ON adtr.access_roles FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_groups_no_truncate BEFORE TRUNCATE ON adtr.resource_groups FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_members_no_truncate BEFORE TRUNCATE ON adtr.resource_group_members FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_role_groups_no_truncate BEFORE TRUNCATE ON adtr.resource_role_groups FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_domains_no_truncate BEFORE TRUNCATE ON adtr.resource_domains FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER task_auth_tenant_no_truncate BEFORE TRUNCATE ON adtr.resource_tenant_config FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
