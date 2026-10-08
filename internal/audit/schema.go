package audit

// Schema is migration 6. Old metadata remains NULL: current identities are never
// joined to historical events or backfilled as purported historical facts.
const Schema = `
ALTER TABLE adtr.auth_audit ADD COLUMN audit_username text, ADD COLUMN audit_ip text, ADD COLUMN audit_path text, ADD COLUMN audit_request_id text;
ALTER TABLE adtr.resource_audit ADD COLUMN audit_username text, ADD COLUMN audit_ip text, ADD COLUMN audit_path text, ADD COLUMN audit_request_id text;
ALTER TABLE adtr.task_events ADD COLUMN audit_username text, ADD COLUMN audit_ip text, ADD COLUMN audit_path text, ADD COLUMN audit_request_id text;
CREATE FUNCTION adtr.capture_audit_metadata() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.audit_username := NULLIF(current_setting('adtr.audit_username',true),'');
 NEW.audit_ip := NULLIF(current_setting('adtr.audit_ip',true),'');
 NEW.audit_path := NULLIF(current_setting('adtr.audit_path',true),'');
 NEW.audit_request_id := NULLIF(current_setting('adtr.audit_request_id',true),'');
 RETURN NEW;
END; $$;
CREATE TRIGGER auth_audit_capture BEFORE INSERT ON adtr.auth_audit FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
CREATE TRIGGER resource_audit_capture BEFORE INSERT ON adtr.resource_audit FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
CREATE TRIGGER task_events_capture BEFORE INSERT ON adtr.task_events FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
CREATE INDEX auth_audit_tenant_time ON adtr.auth_audit(tenant_id,occurred_at,id);
CREATE INDEX resource_audit_tenant_time ON adtr.resource_audit(tenant_id,occurred_at,id);
CREATE INDEX task_events_tenant_time ON adtr.task_events(tenant_id,occurred_at,id);
CREATE TABLE adtr.audit_events (
 id bigserial PRIMARY KEY, tenant_id text NOT NULL,actor_id bigint NOT NULL,
 action text NOT NULL CHECK(action IN ('audit_hide','audit_restore','audit_export')),
 target_id text NOT NULL, reason text NOT NULL DEFAULT '',visibility_version bigint NOT NULL DEFAULT 0,
 audit_username text,audit_ip text,audit_path text,audit_request_id text,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TRIGGER audit_events_capture BEFORE INSERT ON adtr.audit_events FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
CREATE TRIGGER audit_events_immutable BEFORE UPDATE OR DELETE ON adtr.audit_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_events_no_truncate BEFORE TRUNCATE ON adtr.audit_events FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE INDEX audit_events_tenant_time ON adtr.audit_events(tenant_id,occurred_at,id);
CREATE TABLE adtr.audit_visibility (
 tenant_id text NOT NULL,source text NOT NULL CHECK(source IN ('auth','resource','task')),source_id bigint NOT NULL CHECK(source_id>0),
 hidden boolean NOT NULL,version bigint NOT NULL CHECK(version>0),PRIMARY KEY(tenant_id,source,source_id)
);
CREATE TABLE adtr.audit_visibility_revisions(tenant_id text PRIMARY KEY,revision bigint NOT NULL CHECK(revision>=0));
` + ViewSchema + `
CREATE TABLE adtr.audit_export_snapshots (
 task_id text PRIMARY KEY REFERENCES adtr.tasks(task_id),tenant_id text NOT NULL,actor_id bigint NOT NULL,authorization_version text NOT NULL,
 visibility_revision bigint NOT NULL,captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),row_count integer NOT NULL,
 columns jsonb NOT NULL CHECK(jsonb_typeof(columns)='array'),CONSTRAINT audit_export_row_limit CHECK(row_count BETWEEN 0 AND 100000)
);
CREATE TABLE adtr.audit_export_rows (
 task_id text NOT NULL REFERENCES adtr.audit_export_snapshots(task_id),ordinal integer NOT NULL CHECK(ordinal BETWEEN 1 AND 100000),
 source text NOT NULL,source_id bigint NOT NULL,domain_id text,row_data jsonb NOT NULL CHECK(jsonb_typeof(row_data)='object'),
 PRIMARY KEY(task_id,ordinal),UNIQUE(task_id,source,source_id)
);
CREATE INDEX audit_export_domains ON adtr.audit_export_rows(task_id,domain_id) WHERE source='task';
CREATE TABLE adtr.audit_export_chunks (
 task_id text NOT NULL REFERENCES adtr.audit_export_snapshots(task_id),fencing_token bigint NOT NULL CHECK(fencing_token>0),chunk_no integer NOT NULL CHECK(chunk_no>=0),
 data bytea NOT NULL CHECK(octet_length(data) BETWEEN 1 AND 1048576),sha256 text NOT NULL,PRIMARY KEY(task_id,fencing_token,chunk_no)
);
CREATE TABLE adtr.audit_export_manifests (
 task_id text NOT NULL REFERENCES adtr.audit_export_snapshots(task_id),fencing_token bigint NOT NULL CHECK(fencing_token>0),
 byte_count bigint NOT NULL CHECK(byte_count BETWEEN 1 AND 134217728),chunk_count integer NOT NULL CHECK(chunk_count>0),row_count integer NOT NULL CHECK(row_count BETWEEN 0 AND 100000),
 sha256 text NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),PRIMARY KEY(task_id,fencing_token)
);
CREATE TRIGGER audit_export_snapshots_immutable BEFORE UPDATE OR DELETE ON adtr.audit_export_snapshots FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_snapshots_no_truncate BEFORE TRUNCATE ON adtr.audit_export_snapshots FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_rows_immutable BEFORE UPDATE OR DELETE ON adtr.audit_export_rows FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_rows_no_truncate BEFORE TRUNCATE ON adtr.audit_export_rows FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_chunks_immutable BEFORE UPDATE OR DELETE ON adtr.audit_export_chunks FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_chunks_no_truncate BEFORE TRUNCATE ON adtr.audit_export_chunks FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_manifests_immutable BEFORE UPDATE OR DELETE ON adtr.audit_export_manifests FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER audit_export_manifests_no_truncate BEFORE TRUNCATE ON adtr.audit_export_manifests FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`

// ViewSchema can be refreshed by later additive migrations without touching history.
const ViewSchema = `
CREATE OR REPLACE FUNCTION adtr.audit_event_deletable(source text,event text) RETURNS boolean LANGUAGE SQL IMMUTABLE AS $$
 SELECT source<>'audit' AND event NOT IN ('schedule.create','schedule.enable','schedule.pause','task.archive','task.restore');
$$;
CREATE OR REPLACE VIEW adtr.audit_source AS
SELECT 'auth'::text source,a.id source_id,a.tenant_id,a.actor_id user_id,a.audit_username login_user,a.audit_ip source_ip,
 CASE WHEN split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update','schedule.create','schedule.enable','schedule.pause','task.archive','task.restore') THEN split_part(a.action,':',1) ELSE a.action END event,
 jsonb_strip_nulls(jsonb_build_object('targetId',a.target_id,'roleId',CASE WHEN split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update') AND position(':' in a.action)>0 THEN substr(a.action,position(':' in a.action)+1) END,'scheduleUUID',CASE WHEN split_part(a.action,':',1) IN ('schedule.create','schedule.enable','schedule.pause') AND position(':' in a.action)>0 THEN split_part(a.action,':',2) END,'operationUUID',CASE WHEN split_part(a.action,':',1) IN ('task.archive','task.restore') AND position(':' in a.action)>0 THEN split_part(a.action,':',2) END,'path',a.audit_path,'requestId',a.audit_request_id)) event_args,
 CASE WHEN right(a.action,7)='_denied' THEN 'FAIL' WHEN (a.action IN ('login','logout','password_change','password_reset','mfa_enroll_begin','mfa_enable','mfa_disable','bootstrap','user_create','user_delete','user_update','user_role_assign','resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm','profile_avatar_update') OR (split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update','schedule.create','schedule.enable','schedule.pause','task.archive','task.restore') AND position(':' in a.action)>0)) THEN 'SUCCESS' ELSE 'NONE' END event_result,
 (right(a.action,7)='_denied' OR (a.action IN ('login','logout','password_change','password_reset','mfa_enroll_begin','mfa_enable','mfa_disable','bootstrap','user_create','user_delete','user_update','user_role_assign','resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm','profile_avatar_update') OR (split_part(a.action,':',1) IN ('role_create','role_update','role_delete','permissions_update','schedule.create','schedule.enable','schedule.pause','task.archive','task.restore') AND position(':' in a.action)>0))) result_available,
 a.occurred_at,CASE WHEN split_part(a.action,':',1) IN ('task.archive','task.restore') THEN 8 ELSE 9 END log_type,NULL::text domain_id,NULL::text task_kind,a.audit_path,a.audit_request_id FROM adtr.auth_audit a
UNION ALL
SELECT 'resource',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('targetId',a.target_id,'path',a.audit_path,'requestId',a.audit_request_id)),
 CASE WHEN a.action IN ('resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm') THEN 'SUCCESS' ELSE 'NONE' END,
 a.action IN ('resource_group_create','resource_group_update','resource_group_delete','resource_roles_replace','resource_tenant_update','system_storage_alarm'),a.occurred_at,9,NULL,NULL,a.audit_path,a.audit_request_id FROM adtr.resource_audit a
UNION ALL
SELECT 'task',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('taskUUID',a.task_id,'state',a.state,'attempt',a.attempt,'path',a.audit_path,'requestId',a.audit_request_id)),
 CASE WHEN a.action IN ('submitted','recovered','kind_rejected','retry_queued','leased','authorization_rejected','attempts_exhausted','started','authorization_stop_requested','progress','completed-with-cancel-race','finished','lease_recovered','cancel_requested') AND a.state='succeeded' THEN 'SUCCESS' WHEN a.action IN ('submitted','recovered','kind_rejected','retry_queued','leased','authorization_rejected','attempts_exhausted','started','authorization_stop_requested','progress','completed-with-cancel-race','finished','lease_recovered','cancel_requested') AND a.state IN ('failed','partial_failed','dead_letter') THEN 'FAIL' ELSE 'NONE' END,
 a.action IN ('submitted','recovered','kind_rejected','retry_queued','leased','authorization_rejected','attempts_exhausted','started','authorization_stop_requested','progress','completed-with-cancel-race','finished','lease_recovered','cancel_requested') AND a.state IN ('queued','running','retry_wait','cancel_requested','succeeded','failed','partial_failed','dead_letter','cancelled'),a.occurred_at,CASE WHEN t.kind='audit.export' THEN 8 ELSE 9 END,a.domain_id,t.kind,a.audit_path,a.audit_request_id FROM adtr.task_events a JOIN adtr.tasks t ON t.task_id=a.task_id AND t.tenant_id=a.tenant_id
UNION ALL
SELECT 'audit',a.id,a.tenant_id,a.actor_id,a.audit_username,a.audit_ip,a.action,
 jsonb_strip_nulls(jsonb_build_object('targetId',a.target_id,'reason',NULLIF(a.reason,''),'visibilityVersion',a.visibility_version,'path',a.audit_path,'requestId',a.audit_request_id)),
 'SUCCESS',true,a.occurred_at,8,NULL,NULL,a.audit_path,a.audit_request_id FROM adtr.audit_events a;
`
