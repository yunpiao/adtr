package domains

// DirectoryUseSchema is the migration-15 directory credential-use fragment.
// It needs DirectoryPurposeSchema and the directory permission mark first.
// DirectoryDependencySchema supplies the typed revision constraint, immutable
// dependency guard and unique object_id index. Integrating migrations
// must reject every unresolved opened use at upgrade before changing schemas.
// This fragment intentionally leaves the shared guards unchanged: without the
// typed dependency extension a reservation cannot commit, so absent integration
// fails closed. Consumer registration and version stamping remain owned by their callers.
const DirectoryUseSchema = `
-- Unknown prior tasks/dependencies must retain their original provenance and
-- blocker semantics. Refuse the whole fragment instead of adopting them.
DO $$ BEGIN
 IF EXISTS(SELECT FROM adtr.tasks WHERE kind='domain.directory_read') THEN
  RAISE EXCEPTION 'reserved directory task kind already exists' USING ERRCODE='23514',CONSTRAINT='domain_directory_task_kind_reserved';
 END IF;
 IF EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read') THEN
  RAISE EXCEPTION 'reserved directory dependency kind already exists' USING ERRCODE='23514',CONSTRAINT='domain_directory_dependency_kind_reserved';
 END IF;
END; $$;
CREATE TABLE adtr.domain_directory_task_uses (
 tenant_id text NOT NULL, domain_id text NOT NULL, task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 account_id text NOT NULL, account_credential_revision bigint NOT NULL CHECK(account_credential_revision>0),
 connection_revision bigint NOT NULL CHECK(connection_revision>0), connection_credential_generation bigint NOT NULL CHECK(connection_credential_generation>0),
 policy_revision text NOT NULL CHECK(policy_revision ~ '^[0-9a-f]{64}$'),
 grant_role_id text NOT NULL, grant_revision bigint NOT NULL CHECK(grant_revision>0), actor_id bigint NOT NULL CHECK(actor_id>0),
 purpose text NOT NULL CHECK(purpose='domain.directory_read'),
 state text NOT NULL CHECK(state IN ('reserved','opened','quiesced')),
 reserved_at timestamptz NOT NULL DEFAULT clock_timestamp(), opened_at timestamptz, quiesced_at timestamptz,
 opener_owner text, opener_fencing_token bigint, opener_attempt integer,
 quiescence_reason text CHECK(quiescence_reason IN ('executor_returned','never_opened_terminal')),
 PRIMARY KEY(tenant_id,domain_id,task_id), UNIQUE(task_id),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id),
 CHECK((state='reserved' AND opened_at IS NULL AND quiesced_at IS NULL AND opener_owner IS NULL AND opener_fencing_token IS NULL AND opener_attempt IS NULL AND quiescence_reason IS NULL)
 OR (state='opened' AND opened_at IS NOT NULL AND quiesced_at IS NULL AND opener_owner IS NOT NULL AND opener_owner<>'' AND opener_fencing_token IS NOT NULL AND opener_fencing_token>0 AND opener_attempt IS NOT NULL AND opener_attempt=1 AND quiescence_reason IS NULL)
 OR (state='quiesced' AND quiesced_at IS NOT NULL AND quiescence_reason IS NOT NULL AND ((quiescence_reason='executor_returned' AND opened_at IS NOT NULL AND opener_owner IS NOT NULL AND opener_owner<>'' AND opener_fencing_token IS NOT NULL AND opener_fencing_token>0 AND opener_attempt IS NOT NULL AND opener_attempt=1)
 OR (quiescence_reason='never_opened_terminal' AND opened_at IS NULL AND opener_owner IS NULL AND opener_fencing_token IS NULL AND opener_attempt IS NULL))))
);
CREATE INDEX domain_directory_uses_reserved ON adtr.domain_directory_task_uses(reserved_at,task_id) WHERE state='reserved';
CREATE FUNCTION adtr.domain_directory_use_pins(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT FROM adtr.tasks t WHERE t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id AND t.actor_id=u.actor_id
 AND t.kind='domain.directory_read' AND t.payload_version=1 AND t.max_attempts=1 AND t.parent_task_id=''
 AND t.payload IS NOT DISTINCT FROM jsonb_build_object('credentialSource','operation_account','connectionRevision',u.connection_revision::text,
 'connectionCredentialGeneration',u.connection_credential_generation::text,'accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,
 'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text,'policyRevision',u.policy_revision));
$$;
CREATE FUNCTION adtr.domain_directory_use_current(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT adtr.credential_use_role_eligible_for_purpose(u.tenant_id,u.domain_id,u.grant_role_id,'domain.directory_read')
 AND EXISTS(SELECT FROM adtr.users a JOIN adtr.tasks t ON t.tenant_id=a.tenant_id AND t.actor_id=a.id AND t.authorization_version=a.authorization_version::text
 WHERE a.tenant_id=u.tenant_id AND a.id=u.actor_id AND t.task_id=u.task_id AND COALESCE(NULLIF(a.role_id,''),a.role)=u.grant_role_id AND NOT a.disabled AND NOT a.must_change AND a.password_updated_at>clock_timestamp()-interval '90 days')
 AND EXISTS(SELECT FROM adtr.domain_connections c WHERE c.tenant_id=u.tenant_id AND c.domain_id=u.domain_id AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND c.operation_account_id=u.account_id AND c.operation_account_credential_revision=u.account_credential_revision AND c.connection_revision=u.connection_revision AND c.credential_revision=u.connection_credential_generation)
 AND EXISTS(SELECT FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.connection_binding' AND d.object_id=u.domain_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation)
 AND EXISTS(SELECT FROM adtr.operation_accounts a JOIN adtr.operation_account_credentials p ON p.tenant_id=a.tenant_id AND p.domain_id=a.domain_id AND p.account_id=a.account_id AND p.credential_revision=a.credential_revision WHERE a.tenant_id=u.tenant_id AND a.domain_id=u.domain_id AND a.account_id=u.account_id AND a.credential_revision=u.account_credential_revision AND a.deleted_at IS NULL AND p.envelope_version=2)
 AND EXISTS(SELECT FROM adtr.operation_account_use_grants g WHERE g.tenant_id=u.tenant_id AND g.domain_id=u.domain_id AND g.account_id=u.account_id AND g.role_id=u.grant_role_id AND g.purpose=u.purpose AND g.allowed AND g.grant_revision=u.grant_revision AND g.account_credential_revision=u.account_credential_revision);
$$;

CREATE FUNCTION adtr.domain_directory_use_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t adtr.tasks%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'directory use history is durable' USING ERRCODE='42501'; END IF;
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 SELECT * INTO t FROM adtr.tasks WHERE task_id=NEW.task_id FOR UPDATE NOWAIT;
 PERFORM 1 FROM adtr.domain_connections WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id FOR UPDATE NOWAIT;
 PERFORM 1 FROM adtr.operation_accounts WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id AND account_id=NEW.account_id FOR UPDATE NOWAIT;
 IF NOT adtr.domain_directory_use_pins(NEW) THEN RAISE EXCEPTION 'directory use task pins mismatch' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'reserved' OR t.state<>'queued' OR t.attempt<>0 OR NOT adtr.domain_directory_use_current(NEW) THEN RAISE EXCEPTION 'directory use admission denied' USING ERRCODE='23514'; END IF;RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-ARRAY['state','opened_at','quiesced_at','opener_owner','opener_fencing_token','opener_attempt','quiescence_reason']) IS DISTINCT FROM
 (to_jsonb(OLD)-ARRAY['state','opened_at','quiesced_at','opener_owner','opener_fencing_token','opener_attempt','quiescence_reason']) THEN RAISE EXCEPTION 'directory use pins are immutable' USING ERRCODE='42501'; END IF;
 IF OLD.state='reserved' AND NEW.state='opened' THEN
  IF t.state<>'running' OR t.lease_until<=clock_timestamp() OR t.lease_until IS NULL OR ROW(NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt) IS DISTINCT FROM ROW(t.lease_owner,t.fencing_token,t.attempt)
  OR NOT adtr.domain_directory_use_current(NEW) THEN RAISE EXCEPTION 'directory use opening denied' USING ERRCODE='23514';END IF;
 ELSIF OLD.state='opened' AND NEW.state='quiesced' THEN
  IF current_setting('adtr.directory_use_protocol',true) IS DISTINCT FROM 'directory_use_cleanup_v1'
  OR current_setting('adtr.directory_use_task',true) IS DISTINCT FROM OLD.task_id
  OR current_setting('adtr.directory_use_owner',true) IS DISTINCT FROM OLD.opener_owner
  OR current_setting('adtr.directory_use_fence',true) IS DISTINCT FROM OLD.opener_fencing_token::text
  OR current_setting('adtr.directory_use_attempt',true) IS DISTINCT FROM OLD.opener_attempt::text
  OR NEW.quiescence_reason IS DISTINCT FROM 'executor_returned'
  OR ROW(NEW.opened_at,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt) IS DISTINCT FROM ROW(OLD.opened_at,OLD.opener_owner,OLD.opener_fencing_token,OLD.opener_attempt) THEN RAISE EXCEPTION 'directory use completion evidence mismatch' USING ERRCODE='42501';END IF;
 ELSIF OLD.state='reserved' AND NEW.state='quiesced' THEN
  IF current_setting('adtr.directory_use_protocol',true) IS DISTINCT FROM 'directory_use_reserved_cleanup_v1'
  OR current_setting('adtr.directory_use_task',true) IS DISTINCT FROM OLD.task_id
  OR NEW.quiescence_reason IS DISTINCT FROM 'never_opened_terminal' OR t.state NOT IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
  OR ROW(NEW.opened_at,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt) IS DISTINCT FROM ROW(OLD.opened_at,OLD.opener_owner,OLD.opener_fencing_token,OLD.opener_attempt) THEN RAISE EXCEPTION 'unopened use is not terminal' USING ERRCODE='42501';END IF;
 ELSE RAISE EXCEPTION 'directory use transition denied' USING ERRCODE='42501';END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_directory_use_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.domain_directory_task_uses FOR EACH ROW EXECUTE FUNCTION adtr.domain_directory_use_guard();
CREATE TRIGGER domain_directory_uses_no_truncate BEFORE TRUNCATE ON adtr.domain_directory_task_uses FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();

CREATE FUNCTION adtr.domain_directory_use_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE id text; u adtr.domain_directory_task_uses%ROWTYPE; n bigint; matched boolean;
BEGIN
 IF TG_TABLE_NAME='tasks' THEN IF NEW.kind<>'domain.directory_read' THEN RETURN NULL;END IF;id:=NEW.task_id;
 ELSIF TG_TABLE_NAME='operation_account_dependencies' THEN
  IF TG_OP='DELETE' THEN IF OLD.consumer_kind<>'domain.directory_read' THEN RETURN NULL;END IF;id:=OLD.object_id;
  ELSE IF NEW.consumer_kind<>'domain.directory_read' THEN RETURN NULL;END IF;id:=NEW.object_id;END IF;
 ELSE id:=NEW.task_id;END IF;
 SELECT * INTO u FROM adtr.domain_directory_task_uses WHERE task_id=id;
 IF NOT FOUND OR NOT adtr.domain_directory_use_pins(u) THEN RAISE EXCEPTION 'directory use reservation missing or inconsistent' USING ERRCODE='23514';END IF;
 SELECT count(*),COALESCE(bool_and(tenant_id=u.tenant_id AND domain_id=u.domain_id AND account_id=u.account_id AND account_credential_revision=u.account_credential_revision AND connection_credential_generation=u.connection_credential_generation),false)
 INTO n,matched FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read' AND object_id=id;
 IF u.state='quiesced' THEN
  IF n<>0 THEN RAISE EXCEPTION 'quiesced use has dependency' USING ERRCODE='23514';END IF;
 ELSE
  IF n<>1 OR NOT matched OR NOT EXISTS(SELECT FROM adtr.operation_accounts a JOIN adtr.operation_account_credentials p ON p.tenant_id=a.tenant_id AND p.domain_id=a.domain_id AND p.account_id=a.account_id AND p.credential_revision=a.credential_revision
   WHERE a.tenant_id=u.tenant_id AND a.domain_id=u.domain_id AND a.account_id=u.account_id AND a.deleted_at IS NULL AND a.credential_revision=u.account_credential_revision AND p.envelope_version=2) THEN RAISE EXCEPTION 'unresolved use must retain exact pair and dependency' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NULL;
END; $$;
CREATE CONSTRAINT TRIGGER domain_directory_use_consistency AFTER INSERT OR UPDATE ON adtr.domain_directory_task_uses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_directory_use_consistency();
CREATE CONSTRAINT TRIGGER domain_directory_use_dependency_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_dependencies DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_directory_use_consistency();
-- INSERT only: Claim/RecoverExpired never take a tenant/resource lock via this
-- invariant, and terminal status never releases a use or dependency.
CREATE CONSTRAINT TRIGGER domain_directory_task_reservation AFTER INSERT ON adtr.tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_directory_use_consistency();

`
