package credentialuse

// Schema is installed by migration 12. No existing account or builtin role gets
// an implicit allow. These guards are authoritative for old application binaries.
const Schema = `
CREATE SEQUENCE adtr.credential_use_revision AS bigint NO CYCLE;
CREATE TABLE adtr.operation_account_use_grants (
 tenant_id text NOT NULL, domain_id text NOT NULL, account_id text NOT NULL,
 purpose text NOT NULL CHECK(purpose='domain.connection_test'), role_id text NOT NULL CHECK(role_id<>'viewer'),
 custom_role_id text GENERATED ALWAYS AS (CASE WHEN role_id='platform_admin' THEN NULL ELSE role_id END) STORED,
 account_credential_revision bigint NOT NULL CHECK(account_credential_revision>0),
 allowed boolean NOT NULL, grant_revision bigint NOT NULL DEFAULT 0 CHECK(grant_revision>0),
 updated_by bigint NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,domain_id,account_id,purpose,role_id),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id),
 FOREIGN KEY(tenant_id,custom_role_id) REFERENCES adtr.access_roles(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX credential_use_allowed_role ON adtr.operation_account_use_grants(tenant_id,role_id,domain_id) WHERE allowed;
CREATE TABLE adtr.operation_account_use_mutations (
 tenant_id text NOT NULL, actor_id bigint NOT NULL, idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 operation text NOT NULL CHECK(operation IN ('grant','revoke')), fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 domain_id text NOT NULL, account_id text NOT NULL, role_id text NOT NULL, purpose text NOT NULL CHECK(purpose='domain.connection_test'),
 grant_revision bigint NOT NULL CHECK(grant_revision>0), account_credential_revision bigint NOT NULL CHECK(account_credential_revision>0),
 allowed boolean NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,actor_id,idempotency_key), CHECK(allowed=(operation='grant')),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id)
);
CREATE TABLE adtr.operation_account_use_audit (
 id bigserial PRIMARY KEY, tenant_id text NOT NULL, actor_id bigint NOT NULL,
 domain_id text NOT NULL, account_id text NOT NULL, role_id text NOT NULL, purpose text NOT NULL CHECK(purpose='domain.connection_test'),
 action text NOT NULL CHECK(action IN ('credential_use_grant','credential_use_revoke','credential_use_pair_replaced','credential_use_account_deleted','credential_use_role_deleted')),
 old_grant_revision bigint NOT NULL CHECK(old_grant_revision>=0), new_grant_revision bigint NOT NULL CHECK(new_grant_revision>0),
 account_credential_revision bigint NOT NULL CHECK(account_credential_revision>0), allowed boolean NOT NULL,
 result text NOT NULL CHECK(result IN ('saved','revoked')),
 audit_username text,audit_ip text,audit_path text,audit_request_id text, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id)
);
CREATE INDEX credential_use_audit_domain_time ON adtr.operation_account_use_audit(tenant_id,domain_id,occurred_at,id);

-- Existing account/resource triggers also bump epochs. Tighten the shared
-- function so a direct SELECT FOR UPDATE cannot invert an actor/account lock
-- against any trigger-originated bump, including accounts with no use grants.
CREATE OR REPLACE FUNCTION adtr.task_auth_bump(tenant text,roles text[]) RETURNS void LANGUAGE plpgsql AS $$
DECLARE actor bigint;
BEGIN
 PERFORM adtr.task_auth_lock(tenant);
 FOR actor IN SELECT id FROM adtr.users WHERE tenant_id=tenant
  AND (roles IS NULL OR COALESCE(NULLIF(role_id,''),role)=ANY(roles)) ORDER BY id FOR UPDATE NOWAIT
 LOOP
  UPDATE adtr.users SET authorization_version=nextval('adtr.task_authorization_epoch') WHERE id=actor;
 END LOOP;
END; $$;

CREATE FUNCTION adtr.credential_use_deny() RETURNS void LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'credential_use_governance_required' USING ERRCODE='42501',CONSTRAINT='credential_use_governance'; END; $$;

CREATE FUNCTION adtr.credential_use_tenant_eligible(t text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT FROM adtr.resource_tenant_config c WHERE c.tenant_id=t AND c.expire_time>extract(epoch FROM statement_timestamp()) AND c.max_ad_count>0
 AND (SELECT count(*) FROM adtr.resource_domains d WHERE d.tenant_id=t AND d.active)<=c.max_ad_count);
$$;
CREATE FUNCTION adtr.credential_use_role_eligible(t text,d text,r text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT r<>'viewer' AND adtr.credential_use_tenant_eligible(t)
 AND (r='platform_admin' OR EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=t AND id=r))
 AND EXISTS(SELECT FROM adtr.resource_domains rd JOIN adtr.domain_connections dc ON dc.tenant_id=rd.tenant_id AND dc.domain_id=rd.id
 JOIN adtr.resource_group_members m ON m.tenant_id=rd.tenant_id AND m.domain_id=rd.id JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id
 WHERE rd.tenant_id=t AND rd.id=d AND rd.active AND dc.deleted_at IS NULL AND rg.role_id=r)
 AND (r='platform_admin' OR (SELECT count(*) FROM adtr.access_permissions WHERE tenant_id=t AND role_id=r
 AND ((mark IN ('domains','tasks') AND readable AND writeable) OR (mark='operation_accounts' AND readable)))=3);
$$;

-- Snapshot only persisted scope. Eligibility is checked independently for every
-- protected write. A statement trigger captures before a delete/reinsert API can
-- remove its own administrator membership. The tenant lock makes the snapshot
-- stable against concurrent management; each trusted helper clears it anew.
CREATE FUNCTION adtr.credential_use_capture_scope() RETURNS void LANGUAGE plpgsql AS $$
DECLARE t text; a text; domains jsonb; cached text;
BEGIN
 IF current_setting('adtr.credential_use_protocol',true) IS DISTINCT FROM 'credential_governance_v1' THEN RETURN; END IF;
 t:=current_setting('adtr.credential_use_tenant',true); a:=current_setting('adtr.credential_use_actor',true);
 IF t IS NULL OR t='' OR a IS NULL OR a !~ '^[1-9][0-9]{0,18}$' THEN PERFORM adtr.credential_use_deny(); END IF;
 PERFORM adtr.task_auth_lock(t);
 cached:=current_setting('adtr.credential_use_scope',true);
 IF cached IS NOT NULL AND cached<>'' THEN RETURN; END IF;
 SELECT COALESCE(jsonb_agg(id ORDER BY id),'[]'::jsonb) INTO domains FROM (
 SELECT DISTINCT rd.id FROM adtr.resource_domains rd JOIN adtr.resource_group_members m ON m.tenant_id=rd.tenant_id AND m.domain_id=rd.id
 JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id
 WHERE rd.tenant_id=t AND rd.active AND rg.role_id='platform_admin') scoped;
 PERFORM set_config('adtr.credential_use_scope',jsonb_build_object('protocol','credential_governance_v1','tenant',t,'actor',a,'operation',current_setting('adtr.credential_use_operation',true),'domains',domains)::text,true);
END; $$;
CREATE FUNCTION adtr.credential_use_capture_statement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM adtr.credential_use_capture_scope(); RETURN NULL; END; $$;

CREATE FUNCTION adtr.credential_use_require(t text,d text,operations text[]) RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE actor text; identity bigint; snapshot jsonb; raw text;
BEGIN
 PERFORM adtr.task_auth_lock(t);
 IF current_setting('adtr.credential_use_protocol',true) IS DISTINCT FROM 'credential_governance_v1'
 OR current_setting('adtr.credential_use_tenant',true) IS DISTINCT FROM t
 OR NOT COALESCE(current_setting('adtr.credential_use_operation',true)=ANY(operations),false) THEN PERFORM adtr.credential_use_deny(); END IF;
 actor:=current_setting('adtr.credential_use_actor',true);
 IF actor IS NULL OR actor !~ '^[1-9][0-9]{0,18}$' THEN PERFORM adtr.credential_use_deny(); END IF;
 SELECT id INTO identity FROM adtr.users WHERE tenant_id=t AND id::text=actor AND role='platform_admin' AND role_id='' AND NOT disabled AND NOT must_change
 AND password_updated_at>clock_timestamp()-interval '90 days' AND mfa_secret<>'';
 IF identity IS NULL OR (current_setting('adtr.credential_use_operation',true) NOT IN ('revoke','manage_tenant') AND NOT adtr.credential_use_tenant_eligible(t)) THEN PERFORM adtr.credential_use_deny(); END IF;
 PERFORM adtr.credential_use_capture_scope();
 raw:=current_setting('adtr.credential_use_scope',true);
 BEGIN snapshot:=raw::jsonb; EXCEPTION WHEN OTHERS THEN PERFORM adtr.credential_use_deny(); END;
 IF snapshot IS NULL OR jsonb_typeof(snapshot)<>'object' OR snapshot->>'protocol' IS DISTINCT FROM 'credential_governance_v1'
 OR snapshot->>'tenant' IS DISTINCT FROM t OR snapshot->>'actor' IS DISTINCT FROM actor OR snapshot->>'operation' IS DISTINCT FROM current_setting('adtr.credential_use_operation',true) OR jsonb_typeof(snapshot->'domains') IS DISTINCT FROM 'array' THEN PERFORM adtr.credential_use_deny(); END IF;
 -- Validate structure before expanding the array; SQL OR evaluation order is
 -- not a safety boundary for a malformed cached context.
 IF EXISTS(SELECT FROM jsonb_array_elements(snapshot->'domains') v WHERE jsonb_typeof(v)<>'string')
 OR NOT COALESCE((snapshot->'domains') ? d,false) THEN PERFORM adtr.credential_use_deny(); END IF;
 RETURN identity;
END; $$;
CREATE FUNCTION adtr.credential_use_require_role(t text,r text,operations text[]) RETURNS void LANGUAGE plpgsql AS $$
DECLARE d text;
BEGIN
 PERFORM adtr.task_auth_lock(t);
 FOR d IN SELECT DISTINCT domain_id FROM adtr.operation_account_use_grants WHERE tenant_id=t AND role_id=r AND allowed ORDER BY domain_id LOOP
  PERFORM adtr.credential_use_require(t,d,operations);
 END LOOP;
END; $$;
CREATE FUNCTION adtr.credential_use_bump(t text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE actor bigint;
BEGIN
 PERFORM adtr.task_auth_lock(t);
 -- Never wait for a row after a trigger already acquired a grant/account row.
 FOR actor IN SELECT id FROM adtr.users WHERE tenant_id=t ORDER BY id FOR UPDATE NOWAIT LOOP
  UPDATE adtr.users SET authorization_version=nextval('adtr.task_authorization_epoch') WHERE id=actor;
 END LOOP;
END; $$;

CREATE FUNCTION adtr.credential_use_grant_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text; a text; current_revision bigint; dead boolean; auto_revoke boolean; actor bigint;
BEGIN
 IF TG_OP='DELETE' THEN t:=OLD.tenant_id;d:=OLD.domain_id;a:=OLD.account_id; ELSE t:=NEW.tenant_id;d:=NEW.domain_id;a:=NEW.account_id; END IF;
 PERFORM adtr.task_auth_lock(t);
 PERFORM 1 FROM adtr.domain_connections WHERE tenant_id=t AND domain_id=d FOR UPDATE NOWAIT;
 SELECT credential_revision,deleted_at IS NOT NULL INTO current_revision,dead FROM adtr.operation_accounts WHERE tenant_id=t AND domain_id=d AND account_id=a FOR UPDATE NOWAIT;
 IF TG_OP='DELETE' THEN
  -- Only the custom-role FK cascade may remove a durable grant incarnation.
  IF pg_trigger_depth()<=1 OR OLD.role_id='platform_admin' OR EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=t AND id=OLD.role_id) THEN PERFORM adtr.credential_use_deny(); END IF;
  RETURN OLD;
 END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.domain_id,NEW.account_id,NEW.purpose,NEW.role_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.domain_id,OLD.account_id,OLD.purpose,OLD.role_id,OLD.created_at) THEN PERFORM adtr.credential_use_deny(); END IF;
 auto_revoke:=false;
 IF TG_OP='UPDATE' THEN
  IF NEW.grant_revision<>OLD.grant_revision THEN PERFORM adtr.credential_use_deny(); END IF;
  auto_revoke:=pg_trigger_depth()>1 AND OLD.allowed AND NOT NEW.allowed AND NEW.account_credential_revision=OLD.account_credential_revision AND (dead OR current_revision<>OLD.account_credential_revision);
  IF ROW(NEW.allowed,NEW.account_credential_revision) IS NOT DISTINCT FROM ROW(OLD.allowed,OLD.account_credential_revision) THEN
   IF (to_jsonb(NEW)-'custom_role_id') IS DISTINCT FROM (to_jsonb(OLD)-'custom_role_id') THEN PERFORM adtr.credential_use_deny(); END IF; RETURN NEW;
  END IF;
 ELSE IF NEW.grant_revision<>0 OR NOT NEW.allowed THEN PERFORM adtr.credential_use_deny(); END IF; END IF;
 IF NOT auto_revoke THEN
  IF NEW.allowed THEN actor:=adtr.credential_use_require(t,d,ARRAY['grant']); ELSE actor:=adtr.credential_use_require(t,d,ARRAY['revoke']); END IF;
  IF NEW.updated_by<>actor THEN PERFORM adtr.credential_use_deny(); END IF;
 ELSE
  actor:=NULL;
  IF current_setting('adtr.credential_use_protocol',true)='credential_governance_v1' AND current_setting('adtr.credential_use_tenant',true)=t THEN
   SELECT id INTO actor FROM adtr.users WHERE tenant_id=t AND id::text=current_setting('adtr.credential_use_actor',true);
  END IF;
  NEW.updated_by:=COALESCE(actor,0);
 END IF;
 IF NEW.allowed AND (dead OR current_revision IS NULL OR NEW.account_credential_revision<>current_revision OR NOT adtr.credential_use_role_eligible(t,d,NEW.role_id)) THEN PERFORM adtr.credential_use_deny(); END IF;
 IF TG_OP='UPDATE' AND NOT NEW.allowed AND NEW.account_credential_revision<>OLD.account_credential_revision THEN PERFORM adtr.credential_use_deny(); END IF;
 NEW.grant_revision:=nextval('adtr.credential_use_revision'); NEW.updated_at:=clock_timestamp();
 RETURN NEW;
END; $$;
CREATE TRIGGER credential_use_grant_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.operation_account_use_grants FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_grant_guard();

CREATE FUNCTION adtr.credential_use_grant_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g adtr.operation_account_use_grants%ROWTYPE; old_revision bigint; new_revision bigint; event text; actor bigint; dead boolean; account_revision bigint;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP='DELETE' THEN g:=OLD;old_revision:=OLD.grant_revision;new_revision:=nextval('adtr.credential_use_revision');event:='credential_use_role_deleted';g.allowed:=false;
 ELSE g:=NEW;new_revision:=NEW.grant_revision;IF TG_OP='INSERT' THEN old_revision:=0;ELSE old_revision:=OLD.grant_revision;END IF;
  event:=CASE WHEN NEW.allowed THEN 'credential_use_grant' ELSE 'credential_use_revoke' END;
  SELECT credential_revision,deleted_at IS NOT NULL INTO account_revision,dead FROM adtr.operation_accounts WHERE tenant_id=g.tenant_id AND domain_id=g.domain_id AND account_id=g.account_id;
  IF NOT g.allowed AND (dead OR account_revision<>g.account_credential_revision) THEN event:=CASE WHEN dead THEN 'credential_use_account_deleted' ELSE 'credential_use_pair_replaced' END; END IF;
 END IF;
 actor:=0;
 IF current_setting('adtr.credential_use_protocol',true)='credential_governance_v1' AND current_setting('adtr.credential_use_tenant',true)=g.tenant_id THEN
  SELECT id INTO actor FROM adtr.users WHERE tenant_id=g.tenant_id AND id::text=current_setting('adtr.credential_use_actor',true);
 END IF;
 INSERT INTO adtr.operation_account_use_audit(tenant_id,actor_id,domain_id,account_id,role_id,purpose,action,old_grant_revision,new_grant_revision,account_credential_revision,allowed,result)
 VALUES(g.tenant_id,COALESCE(actor,0),g.domain_id,g.account_id,g.role_id,g.purpose,event,old_revision,new_revision,g.account_credential_revision,g.allowed,CASE WHEN g.allowed THEN 'saved' ELSE 'revoked' END);
 PERFORM adtr.credential_use_bump(g.tenant_id);
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER credential_use_grant_event AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_use_grants FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_grant_event();
CREATE FUNCTION adtr.credential_use_account_invalidate() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.credential_revision,NEW.deleted_at) IS DISTINCT FROM ROW(OLD.credential_revision,OLD.deleted_at) THEN
  PERFORM adtr.task_auth_lock(NEW.tenant_id);
  UPDATE adtr.operation_account_use_grants SET allowed=false WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id AND account_id=NEW.account_id AND allowed;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER credential_use_account_invalidate AFTER UPDATE ON adtr.operation_accounts FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_account_invalidate();

CREATE FUNCTION adtr.credential_use_management_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE row_data jsonb; t text; r text; gid text; side integer;
BEGIN
 FOR side IN 1..2 LOOP
  IF side=1 THEN IF TG_OP='INSERT' THEN CONTINUE; END IF; row_data:=to_jsonb(OLD);
  ELSE IF TG_OP='DELETE' THEN CONTINUE; END IF; row_data:=to_jsonb(NEW); END IF;
  t:=row_data->>'tenant_id'; PERFORM adtr.task_auth_lock(t);
  IF TG_TABLE_NAME IN ('access_permissions','resource_role_groups') THEN
   PERFORM adtr.credential_use_require_role(t,row_data->>'role_id',ARRAY['manage_roles']);
  ELSIF TG_TABLE_NAME='access_roles' THEN
   -- Role name/remark edits alone do not convey authority.
   IF TG_OP='DELETE' THEN PERFORM adtr.credential_use_require_role(t,row_data->>'id',ARRAY['manage_roles']); END IF;
  ELSIF TG_TABLE_NAME IN ('resource_group_members','resource_groups') THEN
   IF TG_TABLE_NAME='resource_groups' THEN gid:=row_data->>'id'; ELSE gid:=row_data->>'group_id'; END IF;
   FOR r IN SELECT role_id FROM adtr.resource_role_groups WHERE tenant_id=t AND resource_role_groups.group_id=gid LOOP
    PERFORM adtr.credential_use_require_role(t,r,ARRAY['manage_roles']);
   END LOOP;
  END IF;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER credential_use_permissions BEFORE INSERT OR UPDATE OR DELETE ON adtr.access_permissions FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_management_guard();
CREATE TRIGGER credential_use_role_groups BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_role_groups FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_management_guard();
CREATE TRIGGER credential_use_group_members BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_group_members FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_management_guard();
CREATE TRIGGER credential_use_group_delete BEFORE DELETE ON adtr.resource_groups FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_management_guard();
CREATE TRIGGER credential_use_role_delete BEFORE DELETE ON adtr.access_roles FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_management_guard();

CREATE FUNCTION adtr.credential_use_tenant_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text;
BEGIN
 IF TG_OP='INSERT' THEN t:=NEW.tenant_id; ELSE t:=OLD.tenant_id; END IF;
 PERFORM adtr.task_auth_lock(t);
 IF TG_OP='UPDATE' AND NEW.tenant_id<>OLD.tenant_id THEN PERFORM adtr.credential_use_deny(); END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.max_ad_count,NEW.expire_time) IS NOT DISTINCT FROM ROW(OLD.tenant_id,OLD.max_ad_count,OLD.expire_time) THEN RETURN NEW; END IF;
 -- INSERT also runs on UPSERT. Preserve unrelated name/UID changes while
 -- protecting re-creation after a previously authorized config deletion.
 IF TG_OP='INSERT' AND EXISTS(SELECT FROM adtr.resource_tenant_config c WHERE c.tenant_id=t AND c.max_ad_count=NEW.max_ad_count AND c.expire_time=NEW.expire_time) THEN RETURN NEW; END IF;
 FOR d IN SELECT DISTINCT domain_id FROM adtr.operation_account_use_grants WHERE tenant_id=t AND allowed ORDER BY domain_id LOOP
  PERFORM adtr.credential_use_require(t,d,ARRAY['manage_tenant']);
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER credential_use_tenant_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_tenant_config FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_tenant_guard();

CREATE FUNCTION adtr.credential_use_receipt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actor bigint;
BEGIN
 actor:=adtr.credential_use_require(NEW.tenant_id,NEW.domain_id,ARRAY[NEW.operation]);
 IF actor<>NEW.actor_id OR NOT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id
 AND account_id=NEW.account_id AND role_id=NEW.role_id AND purpose=NEW.purpose AND grant_revision=NEW.grant_revision
 AND account_credential_revision=NEW.account_credential_revision AND allowed=NEW.allowed) THEN PERFORM adtr.credential_use_deny(); END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER credential_use_receipt_guard BEFORE INSERT ON adtr.operation_account_use_mutations FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_receipt_guard();

CREATE FUNCTION adtr.credential_use_user_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_role text; new_role text; security_changed boolean; protected boolean; op text; self_ok boolean;
BEGIN
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 new_role:=COALESCE(NULLIF(NEW.role_id,''),NEW.role);
 IF TG_OP='INSERT' THEN PERFORM adtr.credential_use_require_role(NEW.tenant_id,new_role,ARRAY['manage_roles']);RETURN NEW; END IF;
 old_role:=COALESCE(NULLIF(OLD.role_id,''),OLD.role);
 IF ROW(NEW.role,NEW.role_id) IS DISTINCT FROM ROW(OLD.role,OLD.role_id) OR OLD.disabled AND NOT NEW.disabled THEN
  PERFORM adtr.credential_use_require_role(OLD.tenant_id,old_role,ARRAY['manage_roles']);
  PERFORM adtr.credential_use_require_role(NEW.tenant_id,new_role,ARRAY['manage_roles']);
 END IF;
 security_changed:=ROW(NEW.password_hash,NEW.pass_strength,NEW.must_change,NEW.password_updated_at,NEW.mfa_secret,NEW.mfa_pending,NEW.mfa_pending_until)
 IS DISTINCT FROM ROW(OLD.password_hash,OLD.pass_strength,OLD.must_change,OLD.password_updated_at,OLD.mfa_secret,OLD.mfa_pending,OLD.mfa_pending_until);
 SELECT EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE tenant_id=OLD.tenant_id AND role_id=old_role AND allowed)
 OR EXISTS(SELECT FROM adtr.operation_account_use_grants WHERE tenant_id=NEW.tenant_id AND role_id=new_role AND allowed) INTO protected;
 IF NOT protected THEN RETURN NEW; END IF;
 IF NOT security_changed THEN
  IF NEW.mfa_last_step<OLD.mfa_last_step THEN PERFORM adtr.credential_use_deny(); END IF;
  RETURN NEW;
 END IF;
 op:=current_setting('adtr.credential_use_operation',true);
 self_ok:=current_setting('adtr.credential_use_protocol',true)='credential_governance_v1' AND current_setting('adtr.credential_use_tenant',true)=OLD.tenant_id
 AND current_setting('adtr.credential_use_actor',true)=OLD.id::text AND NOT OLD.disabled
 AND (to_jsonb(NEW)-ARRAY['password_hash','pass_strength','must_change','password_updated_at','mfa_secret','mfa_pending','mfa_pending_until','mfa_last_step','authorization_version','custom_role_id'])
 IS NOT DISTINCT FROM (to_jsonb(OLD)-ARRAY['password_hash','pass_strength','must_change','password_updated_at','mfa_secret','mfa_pending','mfa_pending_until','mfa_last_step','authorization_version','custom_role_id']);
 IF self_ok THEN
  IF op='self_password' AND NOT NEW.must_change AND NEW.password_hash<>OLD.password_hash
   AND ROW(NEW.mfa_secret,NEW.mfa_pending,NEW.mfa_pending_until,NEW.mfa_last_step) IS NOT DISTINCT FROM ROW(OLD.mfa_secret,OLD.mfa_pending,OLD.mfa_pending_until,OLD.mfa_last_step) THEN RETURN NEW; END IF;
  IF ROW(NEW.password_hash,NEW.pass_strength,NEW.must_change,NEW.password_updated_at) IS NOT DISTINCT FROM ROW(OLD.password_hash,OLD.pass_strength,OLD.must_change,OLD.password_updated_at) THEN
   IF op='self_mfa_begin' AND NEW.mfa_secret=OLD.mfa_secret AND NEW.mfa_last_step=OLD.mfa_last_step AND NEW.mfa_pending<>'' AND NEW.mfa_pending_until>clock_timestamp() THEN RETURN NEW; END IF;
   IF op='self_mfa_confirm' AND OLD.mfa_pending<>'' AND OLD.mfa_pending_until>clock_timestamp() AND NEW.mfa_secret=OLD.mfa_pending AND NEW.mfa_pending='' AND NEW.mfa_pending_until IS NULL AND NEW.mfa_last_step>=0 THEN RETURN NEW; END IF;
   IF op='self_mfa_disable' AND NEW.mfa_secret='' AND NEW.mfa_pending='' AND NEW.mfa_pending_until IS NULL AND NEW.mfa_last_step=-1 THEN RETURN NEW; END IF;
  END IF;
 END IF;
 IF op IS DISTINCT FROM 'reset_password' OR NOT NEW.must_change OR NEW.password_hash=OLD.password_hash
 OR (to_jsonb(NEW)-ARRAY['password_hash','pass_strength','must_change','password_updated_at','authorization_version','custom_role_id'])
 IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['password_hash','pass_strength','must_change','password_updated_at','authorization_version','custom_role_id']) THEN PERFORM adtr.credential_use_deny(); END IF;
 PERFORM adtr.credential_use_require_role(OLD.tenant_id,old_role,ARRAY['reset_password']);
 PERFORM adtr.credential_use_require_role(NEW.tenant_id,new_role,ARRAY['reset_password']);
 RETURN NEW;
END; $$;
CREATE TRIGGER credential_use_user_guard BEFORE INSERT OR UPDATE ON adtr.users FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_user_guard();

-- Final-state scope invariant allows delete/reinsert within the transaction but
-- never strands current allows with no administrator able to revoke them.
CREATE FUNCTION adtr.credential_use_admin_scope_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; tenants text[];
BEGIN
 IF TG_OP='DELETE' THEN tenants:=ARRAY[OLD.tenant_id];ELSIF TG_OP='INSERT' THEN tenants:=ARRAY[NEW.tenant_id];ELSE tenants:=ARRAY[OLD.tenant_id,NEW.tenant_id];END IF;
 FOREACH t IN ARRAY tenants LOOP
 PERFORM adtr.task_auth_lock(t);
 IF EXISTS(SELECT FROM adtr.operation_account_use_grants g WHERE g.tenant_id=t AND g.allowed
 AND NOT EXISTS(SELECT FROM adtr.resource_group_members m JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id
 WHERE m.tenant_id=t AND m.domain_id=g.domain_id AND rg.role_id='platform_admin')) THEN PERFORM adtr.credential_use_deny(); END IF;
 END LOOP;
 RETURN NULL;
END; $$;
CREATE CONSTRAINT TRIGGER credential_use_admin_scope_grants AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_use_grants DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_admin_scope_consistency();
CREATE CONSTRAINT TRIGGER credential_use_admin_scope_members AFTER INSERT OR UPDATE OR DELETE ON adtr.resource_group_members DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_admin_scope_consistency();
CREATE CONSTRAINT TRIGGER credential_use_admin_scope_bindings AFTER INSERT OR UPDATE OR DELETE ON adtr.resource_role_groups DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_admin_scope_consistency();

CREATE TRIGGER credential_use_capture_users BEFORE INSERT OR UPDATE ON adtr.users FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_capture_permissions BEFORE INSERT OR UPDATE OR DELETE ON adtr.access_permissions FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_capture_roles BEFORE DELETE ON adtr.access_roles FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_capture_role_groups BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_role_groups FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_capture_members BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_group_members FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_capture_groups BEFORE DELETE ON adtr.resource_groups FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_capture_tenant BEFORE INSERT OR UPDATE OR DELETE ON adtr.resource_tenant_config FOR EACH STATEMENT EXECUTE FUNCTION adtr.credential_use_capture_statement();
CREATE TRIGGER credential_use_grants_no_truncate BEFORE TRUNCATE ON adtr.operation_account_use_grants FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER credential_use_receipts_immutable BEFORE UPDATE OR DELETE ON adtr.operation_account_use_mutations FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER credential_use_receipts_no_truncate BEFORE TRUNCATE ON adtr.operation_account_use_mutations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER credential_use_audit_immutable BEFORE UPDATE OR DELETE ON adtr.operation_account_use_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER credential_use_audit_no_truncate BEFORE TRUNCATE ON adtr.operation_account_use_audit FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER credential_use_audit_capture BEFORE INSERT ON adtr.operation_account_use_audit FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
`
