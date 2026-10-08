package domains

// DirectoryUseV2Schema is an unapplied schema-16 component. Its caller must
// install credentialuse.DirectoryV2PurposeSchema and DirectoryDependencyV2Schema
// atomically under the existing schema fence; this fragment never stamps or
// registers anything. The schema-15 guard remains installed: it calls the shared
// pins/current helpers below and already makes every new provenance field
// immutable. Its original-opener cleanup protocol remains unchanged and never
// consults current grant, role, source or lease authority.
const DirectoryUseV2Schema = `
DO $$ BEGIN
 -- The caller also runs the decisive B2 and directory quiescence preflight
 -- under the same exclusive schema gate. Fail promptly and roll back so the
 -- original executor can acknowledge; never wait for it while holding the gate.
 -- This local assertion prevents installing the fragment around an open use.
 IF EXISTS(SELECT FROM adtr.domain_directory_task_uses WHERE state='opened') THEN
  RAISE EXCEPTION 'directory credential use is opened' USING ERRCODE='23514',CONSTRAINT='domain_directory_v2_opened_use';
 END IF;
 IF EXISTS(SELECT FROM adtr.tasks WHERE kind='domain.directory_read.v2') THEN
  RAISE EXCEPTION 'reserved directory v2 task kind already exists' USING ERRCODE='23514',CONSTRAINT='domain_directory_v2_task_kind_reserved';
 END IF;
 IF EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read.v2') THEN
  RAISE EXCEPTION 'reserved directory v2 dependency kind already exists' USING ERRCODE='23514',CONSTRAINT='domain_directory_v2_dependency_kind_reserved';
 END IF;
 IF EXISTS(SELECT FROM adtr.domain_directory_task_uses u WHERE u.purpose<>'domain.directory_read' OR NOT adtr.domain_directory_use_pins(u))
 OR EXISTS(SELECT FROM adtr.tasks t WHERE t.kind='domain.directory_read' AND NOT EXISTS(SELECT FROM adtr.domain_directory_task_uses u WHERE u.task_id=t.task_id))
 OR EXISTS(SELECT FROM adtr.operation_account_dependencies d WHERE d.consumer_kind='domain.directory_read' AND NOT EXISTS(SELECT FROM adtr.domain_directory_task_uses u WHERE u.task_id=d.object_id))
 OR EXISTS(SELECT FROM adtr.domain_directory_task_uses u WHERE
  (u.state='quiesced' AND EXISTS(SELECT FROM adtr.operation_account_dependencies d WHERE d.consumer_kind='domain.directory_read' AND d.object_id=u.task_id))
  OR (u.state<>'quiesced' AND (SELECT count(*) FROM adtr.operation_account_dependencies d WHERE d.consumer_kind='domain.directory_read' AND d.object_id=u.task_id AND d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation)<>1)) THEN
  RAISE EXCEPTION 'legacy directory identity is inconsistent' USING ERRCODE='23514',CONSTRAINT='domain_directory_v2_legacy_identity';
 END IF;
END; $$;
-- Do not update tasks or observations. Historical payload bytes, body bytes,
-- digests, timestamps and grant history retain their original meaning.
-- ADD without IF NOT EXISTS rejects every partial/foreign provenance column.
ALTER TABLE adtr.domain_directory_task_uses ADD COLUMN dictionary_version integer NOT NULL DEFAULT 1;
ALTER TABLE adtr.domain_directory_task_uses DROP CONSTRAINT domain_directory_task_uses_purpose_check;
ALTER TABLE adtr.domain_directory_task_uses ADD CONSTRAINT domain_directory_use_profile CHECK(
 (purpose='domain.directory_read' AND dictionary_version=1) OR
 (purpose='domain.directory_read.v2' AND dictionary_version=2));
CREATE OR REPLACE FUNCTION adtr.domain_directory_use_pins(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT ((u.purpose='domain.directory_read' AND u.dictionary_version=1) OR (u.purpose='domain.directory_read.v2' AND u.dictionary_version=2))
 AND EXISTS(SELECT FROM adtr.tasks t WHERE t.task_id=u.task_id AND t.tenant_id=u.tenant_id AND t.domain_id=u.domain_id AND t.actor_id=u.actor_id
 AND t.kind=u.purpose AND t.payload_version=1 AND t.max_attempts=1 AND t.parent_task_id=''
 AND t.payload IS NOT DISTINCT FROM (jsonb_build_object('credentialSource','operation_account','connectionRevision',u.connection_revision::text,
 'connectionCredentialGeneration',u.connection_credential_generation::text,'accountId',u.account_id,'accountCredentialRevision',u.account_credential_revision::text,
 'grantRoleId',u.grant_role_id,'grantRevision',u.grant_revision::text,'policyRevision',u.policy_revision)
 || CASE WHEN u.dictionary_version=2 THEN jsonb_build_object('dictionaryVersion',2) ELSE '{}'::jsonb END));
$$;
CREATE OR REPLACE FUNCTION adtr.domain_directory_use_current(u adtr.domain_directory_task_uses) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT adtr.domain_directory_use_pins(u) AND adtr.credential_use_role_eligible_for_purpose(u.tenant_id,u.domain_id,u.grant_role_id,u.purpose)
 AND EXISTS(SELECT FROM adtr.users a JOIN adtr.tasks t ON t.tenant_id=a.tenant_id AND t.actor_id=a.id AND t.authorization_version=a.authorization_version::text
 WHERE a.tenant_id=u.tenant_id AND a.id=u.actor_id AND t.task_id=u.task_id AND COALESCE(NULLIF(a.role_id,''),a.role)=u.grant_role_id AND NOT a.disabled AND NOT a.must_change AND a.password_updated_at>clock_timestamp()-interval '90 days')
 AND EXISTS(SELECT FROM adtr.domain_connections c WHERE c.tenant_id=u.tenant_id AND c.domain_id=u.domain_id AND c.deleted_at IS NULL AND c.credential_mode='operation_account'
 AND c.operation_account_id=u.account_id AND c.operation_account_credential_revision=u.account_credential_revision AND c.connection_revision=u.connection_revision AND c.credential_revision=u.connection_credential_generation)
 AND EXISTS(SELECT FROM adtr.operation_account_dependencies d WHERE d.tenant_id=u.tenant_id AND d.domain_id=u.domain_id AND d.account_id=u.account_id AND d.consumer_kind='domain.connection_binding' AND d.object_id=u.domain_id AND d.account_credential_revision=u.account_credential_revision AND d.connection_credential_generation=u.connection_credential_generation)
 AND EXISTS(SELECT FROM adtr.operation_accounts a JOIN adtr.operation_account_credentials p ON p.tenant_id=a.tenant_id AND p.domain_id=a.domain_id AND p.account_id=a.account_id AND p.credential_revision=a.credential_revision WHERE a.tenant_id=u.tenant_id AND a.domain_id=u.domain_id AND a.account_id=u.account_id AND a.credential_revision=u.account_credential_revision AND a.deleted_at IS NULL AND p.envelope_version=2)
 AND EXISTS(SELECT FROM adtr.operation_account_use_grants g WHERE g.tenant_id=u.tenant_id AND g.domain_id=u.domain_id AND g.account_id=u.account_id AND g.role_id=u.grant_role_id AND g.purpose=u.purpose AND g.allowed AND g.grant_revision=u.grant_revision AND g.account_credential_revision=u.account_credential_revision);
$$;

CREATE OR REPLACE FUNCTION adtr.domain_directory_use_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE id text; u adtr.domain_directory_task_uses%ROWTYPE; n bigint; matched boolean;
BEGIN
 IF TG_TABLE_NAME='tasks' THEN IF NEW.kind NOT IN ('domain.directory_read','domain.directory_read.v2') THEN RETURN NULL;END IF;id:=NEW.task_id;
 ELSIF TG_TABLE_NAME='operation_account_dependencies' THEN
  IF TG_OP='DELETE' THEN IF OLD.consumer_kind NOT IN ('domain.directory_read','domain.directory_read.v2') THEN RETURN NULL;END IF;id:=OLD.object_id;
  ELSE IF NEW.consumer_kind NOT IN ('domain.directory_read','domain.directory_read.v2') THEN RETURN NULL;END IF;id:=NEW.object_id;END IF;
 ELSE id:=NEW.task_id;END IF;
 SELECT * INTO u FROM adtr.domain_directory_task_uses WHERE task_id=id;
 IF NOT FOUND OR NOT adtr.domain_directory_use_pins(u) THEN RAISE EXCEPTION 'directory use reservation missing or inconsistent' USING ERRCODE='23514';END IF;
 SELECT count(*),COALESCE(bool_and(consumer_kind=u.purpose AND tenant_id=u.tenant_id AND domain_id=u.domain_id AND account_id=u.account_id AND account_credential_revision=u.account_credential_revision AND connection_credential_generation=u.connection_credential_generation),false)
 INTO n,matched FROM adtr.operation_account_dependencies WHERE consumer_kind IN ('domain.directory_read','domain.directory_read.v2') AND object_id=id;
 IF u.state='quiesced' THEN
  IF n<>0 THEN RAISE EXCEPTION 'quiesced use has dependency' USING ERRCODE='23514';END IF;
 ELSE
  IF n<>1 OR NOT matched OR NOT EXISTS(SELECT FROM adtr.operation_accounts a JOIN adtr.operation_account_credentials p ON p.tenant_id=a.tenant_id AND p.domain_id=a.domain_id AND p.account_id=a.account_id AND p.credential_revision=a.credential_revision
   WHERE a.tenant_id=u.tenant_id AND a.domain_id=u.domain_id AND a.account_id=u.account_id AND a.deleted_at IS NULL AND a.credential_revision=u.account_credential_revision AND p.envelope_version=2) THEN RAISE EXCEPTION 'unresolved use must retain exact pair and dependency' USING ERRCODE='23514';END IF;
 END IF;
 RETURN NULL;
END; $$;
`
