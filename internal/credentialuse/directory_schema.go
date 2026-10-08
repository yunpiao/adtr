package credentialuse

// DirectoryPurposeSchema is installed atomically by migration15 after the
// directory_assets permission mark, under the normal schema and
// credential-use quiescence gate. It must not rewrite historical Schema.
// No grants are inserted, copied, or implicitly granted by this extension.
const DirectoryPurposeSchema = `
ALTER TABLE adtr.operation_account_use_grants DROP CONSTRAINT operation_account_use_grants_purpose_check;
ALTER TABLE adtr.operation_account_use_grants ADD CONSTRAINT operation_account_use_grants_purpose_check CHECK(purpose IN ('domain.connection_test','domain.directory_read'));
ALTER TABLE adtr.operation_account_use_mutations DROP CONSTRAINT operation_account_use_mutations_purpose_check;
ALTER TABLE adtr.operation_account_use_mutations ADD CONSTRAINT operation_account_use_mutations_purpose_check CHECK(purpose IN ('domain.connection_test','domain.directory_read'));
ALTER TABLE adtr.operation_account_use_audit DROP CONSTRAINT operation_account_use_audit_purpose_check;
ALTER TABLE adtr.operation_account_use_audit ADD CONSTRAINT operation_account_use_audit_purpose_check CHECK(purpose IN ('domain.connection_test','domain.directory_read'));

-- Preserve the old three-argument function exactly: old connection-test
-- consumers retain their domains-write and operation-account-read requirements.
-- Unknown and NULL purposes return false rather than widening eligibility.
CREATE FUNCTION adtr.credential_use_role_eligible_for_purpose(t text,d text,r text,p text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE p
 WHEN 'domain.connection_test' THEN COALESCE(adtr.credential_use_role_eligible(t,d,r),false)
 WHEN 'domain.directory_read' THEN COALESCE(
 r<>'viewer' AND adtr.credential_use_tenant_eligible(t)
 AND (r='platform_admin' OR EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=t AND id=r))
 AND EXISTS(SELECT FROM adtr.resource_domains rd JOIN adtr.domain_connections dc ON dc.tenant_id=rd.tenant_id AND dc.domain_id=rd.id
 JOIN adtr.resource_group_members m ON m.tenant_id=rd.tenant_id AND m.domain_id=rd.id JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id
 WHERE rd.tenant_id=t AND rd.id=d AND rd.active AND dc.deleted_at IS NULL AND rg.role_id=r)
 AND (r='platform_admin' OR (SELECT count(*) FROM adtr.access_permissions WHERE tenant_id=t AND role_id=r
 AND ((mark='domains' AND readable) OR (mark IN ('directory_assets','tasks') AND readable AND writeable)))=3),false)
 ELSE false END;
$$;

-- domains and tasks already have epoch triggers. The new permission mark must
-- invalidate every affected role member on both removal and restoration, even
-- when its explicit credential grant is currently dormant.
CREATE FUNCTION adtr.credential_use_directory_permission_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE ot text; nt text; orole text; nrole text;
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF TG_OP<>'INSERT' AND OLD.mark='directory_assets' THEN ot:=OLD.tenant_id; orole:=OLD.role_id; END IF;
 IF TG_OP<>'DELETE' AND NEW.mark='directory_assets' THEN nt:=NEW.tenant_id; nrole:=NEW.role_id; END IF;
 IF ot IS NOT NULL AND nt IS NOT NULL AND ot<>nt THEN
  PERFORM adtr.task_auth_lock(least(ot,nt)); PERFORM adtr.task_auth_lock(greatest(ot,nt));
 END IF;
 IF ot IS NOT NULL THEN PERFORM adtr.task_auth_bump(ot,ARRAY[orole]); END IF;
 IF nt IS NOT NULL AND (nt IS DISTINCT FROM ot OR nrole IS DISTINCT FROM orole) THEN PERFORM adtr.task_auth_bump(nt,ARRAY[nrole]); END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER credential_use_directory_permission_epoch AFTER INSERT OR UPDATE OR DELETE ON adtr.access_permissions FOR EACH ROW EXECUTE FUNCTION adtr.credential_use_directory_permission_epoch();

-- Replace only purpose-dependent grant eligibility. Preserve the lock order,
-- explicit built-in administrator context, current account/pair pin, immutable
-- grant identity, automatic invalidation exception, and monotonic revisions.
CREATE OR REPLACE FUNCTION adtr.credential_use_grant_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
 IF NEW.purpose IS NULL OR NEW.purpose NOT IN ('domain.connection_test','domain.directory_read') THEN PERFORM adtr.credential_use_deny(); END IF;
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
 IF NEW.allowed AND (dead OR current_revision IS NULL OR NEW.account_credential_revision<>current_revision OR NOT adtr.credential_use_role_eligible_for_purpose(t,d,NEW.role_id,NEW.purpose)) THEN PERFORM adtr.credential_use_deny(); END IF;
 IF TG_OP='UPDATE' AND NOT NEW.allowed AND NEW.account_credential_revision<>OLD.account_credential_revision THEN PERFORM adtr.credential_use_deny(); END IF;
 NEW.grant_revision:=nextval('adtr.credential_use_revision'); NEW.updated_at:=clock_timestamp();
 RETURN NEW;
END; $$;

-- The existing grant event and account invalidation functions already handle
-- every purpose: audit retains g.purpose and account replacement/removal revokes
-- all allowed rows and bumps tenant actor epochs. The receipt guard joins the
-- exact purpose and revision. Their behavior is deliberately not narrowed.
-- All management guards (role/permission/resource membership, tenant, user,
-- deferred admin scope) inspect every allowed grant, including dormant grants,
-- without a purpose predicate. This keeps directory authority under the same
-- fresh-proof governance and scope invariant, including old application writes.
-- Existing immutable-history and no-TRUNCATE protections remain installed.
`
