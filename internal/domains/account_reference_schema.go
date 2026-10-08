package domains

// AccountReferenceSchema is additive migration 13. Historical custom payloads,
// ciphertext, receipts and diagnostic observations are not rewritten.
const AccountReferenceSchema = `
-- Never adopt a previously unknown task as a new secret consumer. A reserved
-- kind collision is a migration blocker with all original data left intact.
DO $$ BEGIN
 IF EXISTS(SELECT FROM adtr.tasks WHERE kind='domain.account_connection_test') THEN
  RAISE EXCEPTION 'reserved account task kind already exists' USING ERRCODE='23514',CONSTRAINT='domain_account_task_kind_reserved';
 END IF;
END; $$;
ALTER TABLE adtr.domain_connections DROP CONSTRAINT domain_connections_credential_mode_check;
ALTER TABLE adtr.domain_connections ADD CONSTRAINT domain_connections_credential_mode_check CHECK(credential_mode IN ('custom','operation_account','unconfigured'));
ALTER TABLE adtr.domain_connections ADD COLUMN operation_account_id text, ADD COLUMN operation_account_credential_revision bigint;
ALTER TABLE adtr.domain_connections ADD CONSTRAINT domain_connection_source_pointer CHECK(
 (credential_mode='operation_account' AND operation_account_id IS NOT NULL AND operation_account_credential_revision IS NOT NULL AND operation_account_credential_revision>0)
 OR (credential_mode<>'operation_account' AND operation_account_id IS NULL AND operation_account_credential_revision IS NULL));
ALTER TABLE adtr.domain_connections ADD CONSTRAINT domain_connection_account_pair FOREIGN KEY(tenant_id,domain_id,operation_account_id,operation_account_credential_revision)
 REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id,credential_revision) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE adtr.domain_creation_receipts ADD COLUMN creation_operation text NOT NULL DEFAULT 'custom' CHECK(creation_operation IN ('custom','unconfigured'));
ALTER TABLE adtr.operation_account_dependencies ADD COLUMN account_credential_revision bigint, ADD COLUMN connection_credential_generation bigint;
ALTER TABLE adtr.operation_account_dependencies ADD CONSTRAINT operation_account_dependency_pins CHECK(
 (consumer_kind IN ('domain.connection_binding','domain.account_connection_test') AND account_credential_revision IS NOT NULL AND account_credential_revision>0 AND connection_credential_generation IS NOT NULL AND connection_credential_generation>0)
 OR (consumer_kind NOT IN ('domain.connection_binding','domain.account_connection_test') AND account_credential_revision IS NULL AND connection_credential_generation IS NULL));
CREATE UNIQUE INDEX domain_connection_binding_identity ON adtr.operation_account_dependencies(tenant_id,domain_id,object_id) WHERE consumer_kind='domain.connection_binding';
CREATE UNIQUE INDEX domain_account_test_use_identity ON adtr.operation_account_dependencies(object_id) WHERE consumer_kind='domain.account_connection_test';
CREATE UNIQUE INDEX domain_test_family_idempotency ON adtr.tasks(tenant_id,domain_id,idempotency_key) WHERE kind IN ('domain.connection_test','domain.account_connection_test');

CREATE TABLE adtr.domain_account_task_uses (
 tenant_id text NOT NULL, domain_id text NOT NULL, task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 account_id text NOT NULL, account_credential_revision bigint NOT NULL CHECK(account_credential_revision>0),
 connection_revision bigint NOT NULL CHECK(connection_revision>0), connection_credential_generation bigint NOT NULL CHECK(connection_credential_generation>0),
 diagnostic_generation bigint NOT NULL CHECK(diagnostic_generation>0), policy_revision text NOT NULL CHECK(policy_revision ~ '^[0-9a-f]{64}$'),
 grant_role_id text NOT NULL, grant_revision bigint NOT NULL CHECK(grant_revision>0), actor_id bigint NOT NULL CHECK(actor_id>0),
 purpose text NOT NULL CHECK(purpose='domain.connection_test'),
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
CREATE INDEX domain_account_uses_reserved ON adtr.domain_account_task_uses(reserved_at,task_id) WHERE state='reserved';
CREATE TABLE adtr.domain_credential_source_mutations (
 tenant_id text NOT NULL, actor_id bigint NOT NULL, idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 operation text NOT NULL CHECK(operation IN ('reference','custom','detach')), fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 domain_id text NOT NULL, connection_revision bigint NOT NULL CHECK(connection_revision>0), connection_credential_generation bigint NOT NULL CHECK(connection_credential_generation>0),
 credential_source text NOT NULL CHECK(credential_source IN ('custom','operation_account','unconfigured')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.domain_connections(tenant_id,domain_id),
 CHECK((operation='reference' AND credential_source='operation_account') OR (operation='custom' AND credential_source='custom') OR (operation='detach' AND credential_source='unconfigured'))
);
ALTER TABLE adtr.domain_audit DROP CONSTRAINT domain_audit_action_check;
ALTER TABLE adtr.domain_audit ADD CONSTRAINT domain_audit_action_check CHECK(action IN ('domain_create','domain_update','domain_delete','domain_test_submit','domain_test_result','domain_credential_reference','domain_credential_custom','domain_credential_detach'));
ALTER TABLE adtr.domain_audit ADD COLUMN credential_source text CHECK(credential_source IN ('custom','operation_account','unconfigured')), ADD COLUMN operation_account_id text, ADD COLUMN account_credential_revision bigint;

-- A separate proof-bound protocol cannot be satisfied by B1 governance or an
-- endpoint-only update. The exact own-role allow is always required for binding.
CREATE FUNCTION adtr.domain_source_require(t text,d text,op text,a text,rev bigint) RETURNS bigint LANGUAGE plpgsql AS $$
DECLARE identity bigint; r text;
BEGIN
 PERFORM adtr.task_auth_lock(t);
 IF current_setting('adtr.domain_source_protocol',true) IS DISTINCT FROM 'account_reference_source_v1'
 OR current_setting('adtr.domain_source_tenant',true) IS DISTINCT FROM t
 OR current_setting('adtr.domain_source_operation',true) IS DISTINCT FROM op
 OR op NOT IN ('reference','custom','detach') THEN RAISE EXCEPTION 'credential_source_authorization_required' USING ERRCODE='42501',CONSTRAINT='domain_source_authorization'; END IF;
 SELECT u.id,COALESCE(NULLIF(u.role_id,''),u.role) INTO identity,r FROM adtr.users u
 WHERE u.tenant_id=t AND u.id::text=current_setting('adtr.domain_source_actor',true)
 AND NOT u.disabled AND NOT u.must_change AND u.password_updated_at>clock_timestamp()-interval '90 days' AND u.mfa_secret<>'' FOR UPDATE NOWAIT;
 IF identity IS NULL OR NOT EXISTS(SELECT FROM adtr.resource_domains rd JOIN adtr.resource_group_members m ON m.tenant_id=rd.tenant_id AND m.domain_id=rd.id
 JOIN adtr.resource_role_groups rg ON rg.tenant_id=m.tenant_id AND rg.group_id=m.group_id WHERE rd.tenant_id=t AND rd.id=d AND rd.active AND rg.role_id=r)
 OR NOT (r='platform_admin' OR EXISTS(SELECT FROM adtr.access_permissions WHERE tenant_id=t AND role_id=r AND mark='domains' AND readable AND writeable))
 OR (op<>'detach' AND NOT adtr.credential_use_tenant_eligible(t)) THEN RAISE EXCEPTION 'credential_source_authorization_required' USING ERRCODE='42501',CONSTRAINT='domain_source_authorization'; END IF;
 IF op='reference' AND (NOT adtr.credential_use_role_eligible(t,d,r)
 OR NOT EXISTS(SELECT FROM adtr.operation_account_use_grants g WHERE g.tenant_id=t AND g.domain_id=d AND g.account_id=a AND g.account_credential_revision=rev AND g.role_id=r AND g.purpose='domain.connection_test' AND g.allowed)) THEN
 RAISE EXCEPTION 'credential_source_authorization_required' USING ERRCODE='42501',CONSTRAINT='domain_source_authorization'; END IF;
 RETURN identity;
END; $$;

CREATE OR REPLACE FUNCTION adtr.domain_connection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed boolean; source_changed boolean; op text;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'domain identity is durable' USING ERRCODE='42501'; END IF;
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 IF TG_OP='INSERT' THEN
  IF NEW.connection_revision<>1 OR NEW.credential_revision<>1 OR NEW.diagnostic_generation<>1 OR NEW.deleted_at IS NOT NULL OR NEW.latest_test_task_id IS NOT NULL OR NEW.credential_mode='operation_account' THEN RAISE EXCEPTION 'invalid initial domain source' USING ERRCODE='23514';END IF;RETURN NEW;
 END IF;
 IF ROW(NEW.tenant_id,NEW.domain_id,NEW.canonical_domain,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.domain_id,OLD.canonical_domain,OLD.created_at)
 OR OLD.deleted_at IS NOT NULL OR NEW.connection_revision<OLD.connection_revision OR NEW.credential_revision<OLD.credential_revision OR NEW.diagnostic_generation<OLD.diagnostic_generation THEN
 RAISE EXCEPTION 'domain identity or revision is immutable' USING ERRCODE='42501'; END IF;
 source_changed:=ROW(NEW.credential_mode,NEW.operation_account_id,NEW.operation_account_credential_revision) IS DISTINCT FROM ROW(OLD.credential_mode,OLD.operation_account_id,OLD.operation_account_credential_revision);
 changed:=source_changed OR ROW(NEW.dc_hostname,NEW.dial_ip,NEW.transport_mode,NEW.port,NEW.credential_revision,NEW.deleted_at) IS DISTINCT FROM ROW(OLD.dc_hostname,OLD.dial_ip,OLD.transport_mode,OLD.port,OLD.credential_revision,OLD.deleted_at);
 IF changed THEN
  IF NEW.connection_revision<>OLD.connection_revision+1 OR NEW.diagnostic_generation<>OLD.diagnostic_generation+1 OR NEW.latest_test_task_id IS NOT NULL THEN RAISE EXCEPTION 'domain revision must advance and observation must clear' USING ERRCODE='23514'; END IF;
 ELSIF NEW.connection_revision<>OLD.connection_revision THEN RAISE EXCEPTION 'domain revision has no change' USING ERRCODE='23514'; END IF;
 IF source_changed OR NEW.deleted_at IS NOT NULL THEN
  IF NEW.credential_revision<>OLD.credential_revision+1 THEN RAISE EXCEPTION 'source generation must advance' USING ERRCODE='23514'; END IF;
 ELSIF NEW.credential_revision<>OLD.credential_revision AND (OLD.credential_mode<>'custom' OR NEW.credential_revision<>OLD.credential_revision+1) THEN RAISE EXCEPTION 'credential revision must advance' USING ERRCODE='23514'; END IF;
 IF NEW.deleted_at IS NOT NULL THEN
  IF NEW.credential_mode<>'unconfigured' OR NEW.operation_account_id IS NOT NULL OR NEW.operation_account_credential_revision IS NOT NULL THEN RAISE EXCEPTION 'deleted source must be unconfigured' USING ERRCODE='23514'; END IF;
 ELSIF source_changed THEN
  op:=CASE NEW.credential_mode WHEN 'operation_account' THEN 'reference' WHEN 'custom' THEN 'custom' ELSE 'detach' END;
  PERFORM adtr.domain_source_require(NEW.tenant_id,NEW.domain_id,op,NEW.operation_account_id,NEW.operation_account_credential_revision);
 END IF;
 IF NOT changed AND ROW(NEW.latest_test_task_id,NEW.diagnostic_generation) IS DISTINCT FROM ROW(OLD.latest_test_task_id,OLD.diagnostic_generation) THEN
  IF NEW.latest_test_task_id IS NULL OR NEW.diagnostic_generation<>OLD.diagnostic_generation+1 OR NOT EXISTS(SELECT FROM adtr.tasks t
   WHERE t.task_id=NEW.latest_test_task_id AND t.tenant_id=NEW.tenant_id AND t.domain_id=NEW.domain_id AND t.payload_version=1
   AND t.kind IN ('domain.connection_test','domain.account_connection_test') AND t.payload->>'connectionRevision' IS NOT DISTINCT FROM NEW.connection_revision::text
   AND t.payload->>'diagnosticGeneration' IS NOT DISTINCT FROM NEW.diagnostic_generation::text) THEN RAISE EXCEPTION 'invalid diagnostic admission' USING ERRCODE='23514'; END IF;
 END IF;
 RETURN NEW;
END; $$;
DROP TRIGGER domain_connection_guard ON adtr.domain_connections;
CREATE TRIGGER domain_connection_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.domain_connections FOR EACH ROW EXECUTE FUNCTION adtr.domain_connection_guard();

-- Domain-pair updates may precede the parent generation change in unchanged
-- legacy UpdateTx. Both the exact +1 and final matching parent are mandatory.
CREATE FUNCTION adtr.domain_pair_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text; c adtr.domain_connections%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN t:=OLD.tenant_id;d:=OLD.domain_id;ELSE t:=NEW.tenant_id;d:=NEW.domain_id;END IF;
 PERFORM adtr.task_auth_lock(t);
 SELECT * INTO c FROM adtr.domain_connections WHERE tenant_id=t AND domain_id=d FOR UPDATE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'domain credential parent missing' USING ERRCODE='23514'; END IF;
 IF TG_OP='UPDATE' AND (ROW(NEW.tenant_id,NEW.domain_id) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.domain_id) OR NEW.credential_revision<>OLD.credential_revision+1) THEN RAISE EXCEPTION 'domain pair replacement must advance generation' USING ERRCODE='23514'; END IF;
 IF TG_OP='DELETE' THEN
  IF c.deleted_at IS NULL AND c.credential_mode='custom' THEN RAISE EXCEPTION 'live custom pair cannot be removed' USING ERRCODE='23514'; END IF;RETURN OLD;
 END IF;
 IF c.deleted_at IS NOT NULL OR c.credential_mode<>'custom' OR NEW.credential_revision NOT IN (c.credential_revision,c.credential_revision+1) THEN RAISE EXCEPTION 'domain credential source mismatch' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_pair_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.domain_credentials FOR EACH ROW EXECUTE FUNCTION adtr.domain_pair_guard();
CREATE TRIGGER domain_credentials_no_truncate BEFORE TRUNCATE ON adtr.domain_credentials FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();

CREATE FUNCTION adtr.domain_source_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text; c adtr.domain_connections%ROWTYPE; pairs bigint; exact_pair boolean; bindings bigint; exact_binding boolean;
BEGIN
 IF TG_TABLE_NAME='operation_account_dependencies' THEN
  IF TG_OP='DELETE' THEN IF OLD.consumer_kind<>'domain.connection_binding' THEN RETURN NULL;END IF;t:=OLD.tenant_id;d:=OLD.domain_id;
  ELSE IF NEW.consumer_kind<>'domain.connection_binding' THEN RETURN NULL;END IF;t:=NEW.tenant_id;d:=NEW.domain_id;END IF;
 ELSE IF TG_OP='DELETE' THEN t:=OLD.tenant_id;d:=OLD.domain_id;ELSE t:=NEW.tenant_id;d:=NEW.domain_id;END IF;END IF;
 SELECT * INTO c FROM adtr.domain_connections WHERE tenant_id=t AND domain_id=d;
 IF NOT FOUND THEN RAISE EXCEPTION 'source parent missing' USING ERRCODE='23514'; END IF;
 SELECT count(*),COALESCE(bool_and(credential_revision=c.credential_revision),false) INTO pairs,exact_pair FROM adtr.domain_credentials WHERE tenant_id=t AND domain_id=d;
 SELECT count(*),COALESCE(bool_and(account_id=c.operation_account_id AND account_credential_revision=c.operation_account_credential_revision AND connection_credential_generation=c.credential_revision AND object_id=d),false)
 INTO bindings,exact_binding FROM adtr.operation_account_dependencies WHERE tenant_id=t AND domain_id=d AND consumer_kind='domain.connection_binding';
 -- Historical deleted custom tombstones remain untouched; no source can retain
 -- ciphertext, pointers or binding after deletion.
 IF c.deleted_at IS NOT NULL OR c.credential_mode='unconfigured' THEN
  IF pairs<>0 OR bindings<>0 OR c.operation_account_id IS NOT NULL OR c.operation_account_credential_revision IS NOT NULL THEN RAISE EXCEPTION 'unconfigured source has credential material' USING ERRCODE='23514';END IF;
 ELSIF c.credential_mode='custom' THEN
  IF pairs<>1 OR NOT exact_pair OR bindings<>0 OR c.operation_account_id IS NOT NULL OR c.operation_account_credential_revision IS NOT NULL THEN RAISE EXCEPTION 'custom source inconsistent' USING ERRCODE='23514';END IF;
 ELSE
  IF pairs<>0 OR bindings<>1 OR NOT exact_binding OR NOT EXISTS(SELECT FROM adtr.operation_accounts a JOIN adtr.operation_account_credentials p ON p.tenant_id=a.tenant_id AND p.domain_id=a.domain_id AND p.account_id=a.account_id AND p.credential_revision=a.credential_revision
   WHERE a.tenant_id=t AND a.domain_id=d AND a.account_id=c.operation_account_id AND a.credential_revision=c.operation_account_credential_revision AND a.deleted_at IS NULL AND p.envelope_version=2) THEN RAISE EXCEPTION 'account source inconsistent' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NULL;
END; $$;
CREATE CONSTRAINT TRIGGER domain_source_consistency AFTER INSERT OR UPDATE ON adtr.domain_connections DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_source_consistency();
CREATE CONSTRAINT TRIGGER domain_pair_source_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.domain_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_source_consistency();
CREATE CONSTRAINT TRIGGER domain_binding_source_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_dependencies DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_source_consistency();
CREATE CONSTRAINT TRIGGER domain_account_source_consistency AFTER INSERT OR UPDATE ON adtr.operation_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_source_consistency();
CREATE CONSTRAINT TRIGGER domain_account_pair_source_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_source_consistency();

CREATE FUNCTION adtr.domain_typed_dependency_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (OLD.consumer_kind IN ('domain.connection_binding','domain.account_connection_test') OR NEW.consumer_kind IN ('domain.connection_binding','domain.account_connection_test')) AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'typed dependency identity is immutable' USING ERRCODE='42501'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
END; $$;
CREATE TRIGGER domain_typed_dependency_guard BEFORE UPDATE ON adtr.operation_account_dependencies FOR EACH ROW EXECUTE FUNCTION adtr.domain_typed_dependency_guard();

CREATE FUNCTION adtr.domain_account_use_pins(u adtr.domain_account_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT FROM adtr.tasks t WHERE t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id AND t.actor_id=u.actor_id
 AND t.kind='domain.account_connection_test' AND t.payload_version=1 AND t.max_attempts=1 AND t.parent_task_id=''
 AND t.payload IS NOT DISTINCT FROM jsonb_build_object('credentialSource','operation_account','connectionRevision',u.connection_revision::text,
 'connectionCredentialGeneration',u.connection_credential_generation::text,'accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,
 'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text,'policyRevision',u.policy_revision,'diagnosticGeneration',u.diagnostic_generation::text));
$$;
CREATE FUNCTION adtr.domain_account_use_current(u adtr.domain_account_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT adtr.credential_use_role_eligible(u.tenant_id,u.domain_id,u.grant_role_id)
 AND EXISTS(SELECT FROM adtr.users a JOIN adtr.tasks t ON t.tenant_id=a.tenant_id AND t.actor_id=a.id AND t.authorization_version=a.authorization_version::text
 WHERE a.tenant_id=u.tenant_id AND a.id=u.actor_id AND t.task_id=u.task_id AND COALESCE(NULLIF(a.role_id,''),a.role)=u.grant_role_id AND NOT a.disabled AND NOT a.must_change AND a.password_updated_at>clock_timestamp()-interval '90 days')
 AND EXISTS(SELECT FROM adtr.domain_connections c WHERE c.tenant_id=u.tenant_id AND c.domain_id=u.domain_id AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND c.operation_account_id=u.account_id AND c.operation_account_credential_revision=u.account_credential_revision AND c.connection_revision=u.connection_revision AND c.credential_revision=u.connection_credential_generation)
 AND EXISTS(SELECT FROM adtr.operation_account_use_grants g WHERE g.tenant_id=u.tenant_id AND g.domain_id=u.domain_id AND g.account_id=u.account_id AND g.role_id=u.grant_role_id AND g.purpose=u.purpose AND g.allowed AND g.grant_revision=u.grant_revision AND g.account_credential_revision=u.account_credential_revision);
$$;

CREATE FUNCTION adtr.domain_account_use_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t adtr.tasks%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'account use history is durable' USING ERRCODE='42501'; END IF;
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 SELECT * INTO t FROM adtr.tasks WHERE task_id=NEW.task_id FOR UPDATE NOWAIT;
 PERFORM 1 FROM adtr.domain_connections WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id FOR UPDATE NOWAIT;
 PERFORM 1 FROM adtr.operation_accounts WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id AND account_id=NEW.account_id FOR UPDATE NOWAIT;
 IF NOT adtr.domain_account_use_pins(NEW) THEN RAISE EXCEPTION 'account use task pins mismatch' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'reserved' OR t.state<>'queued' OR t.attempt<>0 OR NOT adtr.domain_account_use_current(NEW) THEN RAISE EXCEPTION 'account use admission denied' USING ERRCODE='23514'; END IF;RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-ARRAY['state','opened_at','quiesced_at','opener_owner','opener_fencing_token','opener_attempt','quiescence_reason']) IS DISTINCT FROM
 (to_jsonb(OLD)-ARRAY['state','opened_at','quiesced_at','opener_owner','opener_fencing_token','opener_attempt','quiescence_reason']) THEN RAISE EXCEPTION 'account use pins are immutable' USING ERRCODE='42501'; END IF;
 IF OLD.state='reserved' AND NEW.state='opened' THEN
  IF t.state<>'running' OR t.lease_until<=clock_timestamp() OR t.lease_until IS NULL OR ROW(NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt) IS DISTINCT FROM ROW(t.lease_owner,t.fencing_token,t.attempt)
  OR NOT adtr.domain_account_use_current(NEW) OR NOT EXISTS(SELECT FROM adtr.domain_connections c WHERE c.tenant_id=NEW.tenant_id AND c.domain_id=NEW.domain_id AND c.latest_test_task_id=NEW.task_id AND c.diagnostic_generation=NEW.diagnostic_generation) THEN RAISE EXCEPTION 'account use opening denied' USING ERRCODE='23514';END IF;
 ELSIF OLD.state='opened' AND NEW.state='quiesced' THEN
  IF current_setting('adtr.account_use_protocol',true) IS DISTINCT FROM 'account_use_cleanup_v1'
  OR current_setting('adtr.account_use_task',true) IS DISTINCT FROM OLD.task_id
  OR current_setting('adtr.account_use_owner',true) IS DISTINCT FROM OLD.opener_owner
  OR current_setting('adtr.account_use_fence',true) IS DISTINCT FROM OLD.opener_fencing_token::text
  OR current_setting('adtr.account_use_attempt',true) IS DISTINCT FROM OLD.opener_attempt::text
  OR NEW.quiescence_reason IS DISTINCT FROM 'executor_returned'
  OR ROW(NEW.opened_at,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt) IS DISTINCT FROM ROW(OLD.opened_at,OLD.opener_owner,OLD.opener_fencing_token,OLD.opener_attempt) THEN RAISE EXCEPTION 'account use completion evidence mismatch' USING ERRCODE='42501';END IF;
 ELSIF OLD.state='reserved' AND NEW.state='quiesced' THEN
  IF current_setting('adtr.account_use_protocol',true) IS DISTINCT FROM 'account_use_reserved_cleanup_v1'
  OR current_setting('adtr.account_use_task',true) IS DISTINCT FROM OLD.task_id
  OR NEW.quiescence_reason IS DISTINCT FROM 'never_opened_terminal' OR t.state NOT IN ('succeeded','failed','partial_failed','dead_letter','cancelled')
  OR ROW(NEW.opened_at,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt) IS DISTINCT FROM ROW(OLD.opened_at,OLD.opener_owner,OLD.opener_fencing_token,OLD.opener_attempt) THEN RAISE EXCEPTION 'unopened use is not terminal' USING ERRCODE='42501';END IF;
 ELSE RAISE EXCEPTION 'account use transition denied' USING ERRCODE='42501';END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_account_use_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.domain_account_task_uses FOR EACH ROW EXECUTE FUNCTION adtr.domain_account_use_guard();
CREATE TRIGGER domain_account_uses_no_truncate BEFORE TRUNCATE ON adtr.domain_account_task_uses FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();

CREATE FUNCTION adtr.domain_account_use_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE id text; u adtr.domain_account_task_uses%ROWTYPE; n bigint; matched boolean;
BEGIN
 IF TG_TABLE_NAME='tasks' THEN IF NEW.kind<>'domain.account_connection_test' THEN RETURN NULL;END IF;id:=NEW.task_id;
 ELSIF TG_TABLE_NAME='operation_account_dependencies' THEN
  IF TG_OP='DELETE' THEN IF OLD.consumer_kind<>'domain.account_connection_test' THEN RETURN NULL;END IF;id:=OLD.object_id;
  ELSE IF NEW.consumer_kind<>'domain.account_connection_test' THEN RETURN NULL;END IF;id:=NEW.object_id;END IF;
 ELSE id:=NEW.task_id;END IF;
 SELECT * INTO u FROM adtr.domain_account_task_uses WHERE task_id=id;
 IF NOT FOUND OR NOT adtr.domain_account_use_pins(u) THEN RAISE EXCEPTION 'account use reservation missing or inconsistent' USING ERRCODE='23514';END IF;
 SELECT count(*),COALESCE(bool_and(tenant_id=u.tenant_id AND domain_id=u.domain_id AND account_id=u.account_id AND account_credential_revision=u.account_credential_revision AND connection_credential_generation=u.connection_credential_generation),false)
 INTO n,matched FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.account_connection_test' AND object_id=id;
 IF u.state='quiesced' THEN
  IF n<>0 THEN RAISE EXCEPTION 'quiesced use has dependency' USING ERRCODE='23514';END IF;
 ELSE
  IF n<>1 OR NOT matched OR NOT EXISTS(SELECT FROM adtr.operation_accounts a JOIN adtr.operation_account_credentials p ON p.tenant_id=a.tenant_id AND p.domain_id=a.domain_id AND p.account_id=a.account_id AND p.credential_revision=a.credential_revision
   WHERE a.tenant_id=u.tenant_id AND a.domain_id=u.domain_id AND a.account_id=u.account_id AND a.deleted_at IS NULL AND a.credential_revision=u.account_credential_revision AND p.envelope_version=2) THEN RAISE EXCEPTION 'unresolved use must retain exact pair and dependency' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NULL;
END; $$;
CREATE CONSTRAINT TRIGGER domain_account_use_consistency AFTER INSERT OR UPDATE ON adtr.domain_account_task_uses DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_account_use_consistency();
CREATE CONSTRAINT TRIGGER domain_account_use_dependency_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_dependencies DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_account_use_consistency();
-- INSERT only: Claim/RecoverExpired never take a tenant/resource lock via this
-- invariant, and terminal status never releases a use or dependency.
CREATE CONSTRAINT TRIGGER domain_account_task_reservation AFTER INSERT ON adtr.tasks DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.domain_account_use_consistency();

CREATE FUNCTION adtr.domain_source_receipt_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE actor bigint; c adtr.domain_connections%ROWTYPE;
BEGIN
 SELECT * INTO c FROM adtr.domain_connections WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id;
 actor:=adtr.domain_source_require(NEW.tenant_id,NEW.domain_id,NEW.operation,c.operation_account_id,c.operation_account_credential_revision);
 IF actor<>NEW.actor_id OR NOT EXISTS(SELECT FROM adtr.domain_connections WHERE tenant_id=NEW.tenant_id AND domain_id=NEW.domain_id AND connection_revision=NEW.connection_revision AND credential_revision=NEW.connection_credential_generation AND credential_mode=NEW.credential_source AND deleted_at IS NULL) THEN RAISE EXCEPTION 'source receipt inconsistent' USING ERRCODE='23514';END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_source_receipt_guard BEFORE INSERT ON adtr.domain_credential_source_mutations FOR EACH ROW EXECUTE FUNCTION adtr.domain_source_receipt_guard();
CREATE TRIGGER domain_source_receipts_immutable BEFORE UPDATE OR DELETE ON adtr.domain_credential_source_mutations FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_source_receipts_no_truncate BEFORE TRUNCATE ON adtr.domain_credential_source_mutations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
