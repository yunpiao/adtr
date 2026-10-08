package credentialuse

// DirectoryV2PurposeSchema is an unapplied fragment for atomic migration16.
// Its caller must first hold the exclusive schema gate, reject opened credential
// uses and reserved-v2 collisions, and validate the complete historical schema15
// shape. Install this before the versioned directory ledger/observation guards.
// It never stamps a schema version, copies grants, or changes old incarnations.
const DirectoryV2PurposeSchema = `
ALTER TABLE adtr.operation_account_use_grants DROP CONSTRAINT operation_account_use_grants_purpose_check;
ALTER TABLE adtr.operation_account_use_grants ADD CONSTRAINT operation_account_use_grants_purpose_check CHECK(purpose IN ('domain.connection_test','domain.directory_read','domain.directory_read.v2'));
ALTER TABLE adtr.operation_account_use_mutations DROP CONSTRAINT operation_account_use_mutations_purpose_check;
ALTER TABLE adtr.operation_account_use_mutations ADD CONSTRAINT operation_account_use_mutations_purpose_check CHECK(purpose IN ('domain.connection_test','domain.directory_read','domain.directory_read.v2'));
ALTER TABLE adtr.operation_account_use_audit DROP CONSTRAINT operation_account_use_audit_purpose_check;
ALTER TABLE adtr.operation_account_use_audit ADD CONSTRAINT operation_account_use_audit_purpose_check CHECK(purpose IN ('domain.connection_test','domain.directory_read','domain.directory_read.v2'));

-- Preserve the connection-test-only helper unchanged. Both exact directory
-- purposes require the existing directory permission and explicit domain scope;
-- eligibility alone is not a grant. Unknown and NULL purposes remain denied.
CREATE OR REPLACE FUNCTION adtr.credential_use_role_eligible_for_purpose(t text,d text,r text,p text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE
 WHEN p='domain.connection_test' THEN COALESCE(adtr.credential_use_role_eligible(t,d,r),false)
 WHEN p IN ('domain.directory_read','domain.directory_read.v2') THEN COALESCE(
 r<>'viewer' AND adtr.credential_use_tenant_eligible(t)
 AND (r='platform_admin' OR EXISTS(SELECT FROM adtr.access_roles WHERE tenant_id=t AND id=r))
 AND EXISTS(SELECT FROM adtr.resource_domains rd JOIN adtr.domain_connections dc ON dc.tenant_id=rd.tenant_id AND dc.domain_id=rd.id
 JOIN adtr.resource_group_members m ON m.tenant_id=rd.tenant_id AND m.domain_id=rd.id JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id
 WHERE rd.tenant_id=t AND rd.id=d AND rd.active AND dc.deleted_at IS NULL AND rg.role_id=r)
 AND (r='platform_admin' OR (SELECT count(*) FROM adtr.access_permissions WHERE tenant_id=t AND role_id=r
 AND ((mark='domains' AND readable) OR (mark IN ('directory_assets','tasks') AND readable AND writeable)))=3),false)
 ELSE false END;
$$;

-- Only the exact purpose allowlist changes in the existing grant guard.
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
 IF NEW.purpose IS NULL OR NEW.purpose NOT IN ('domain.connection_test','domain.directory_read','domain.directory_read.v2') THEN PERFORM adtr.credential_use_deny(); END IF;
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

-- All other protections remain installed unchanged: grant events and account
-- invalidation operate across purposes; receipts join the exact purpose and
-- incarnation. Existing management, tenant, actor and deferred administrator
-- scope guards protect every allowed grant, including dormant v2 grants.
-- The schema15 directory_assets epoch trigger is reused, never duplicated.
-- Existing immutable-history, no-TRUNCATE and credential-use quiescence gates
-- remain mandatory. No data updates or inserts are part of this fragment.
`
