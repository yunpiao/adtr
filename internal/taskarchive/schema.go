package taskarchive

// Schema follows tasks.ArchiveCoreSchema in explicit migration 8. These
// immutable receipts and control events are separate from executor history.
const Schema = `
CREATE TABLE adtr.task_archive_operations (
 operation_id text PRIMARY KEY, tenant_id text NOT NULL, actor_id bigint NOT NULL,
 authorization_version text NOT NULL CHECK(authorization_version<>''),
 operation_key text NOT NULL, request_hash text NOT NULL,
 action text NOT NULL CHECK(action IN ('archive','restore')),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 occurred_at timestamptz NOT NULL,
 UNIQUE(tenant_id,actor_id,operation_key)
);
CREATE TRIGGER task_archive_operations_immutable BEFORE UPDATE OR DELETE ON adtr.task_archive_operations FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_archive_operations_no_truncate BEFORE TRUNCATE ON adtr.task_archive_operations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TABLE adtr.task_archive_events (
 id bigserial PRIMARY KEY,
 operation_id text NOT NULL REFERENCES adtr.task_archive_operations(operation_id),
 task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 tenant_id text NOT NULL, actor_id bigint NOT NULL, authorization_version text NOT NULL,
 action text NOT NULL CHECK(action IN ('archive','restore')),
 previous_version bigint NOT NULL CHECK(previous_version>=0),
 visibility_version bigint NOT NULL CHECK(visibility_version=previous_version+1),
 archived boolean NOT NULL, reason text NOT NULL CHECK(char_length(reason) BETWEEN 1 AND 500),
 username text NOT NULL DEFAULT COALESCE(current_setting('adtr.audit_username',true),''),
 peer_ip text NOT NULL DEFAULT COALESCE(current_setting('adtr.audit_ip',true),''),
 request_path text NOT NULL DEFAULT COALESCE(current_setting('adtr.audit_path',true),''),
 request_id text NOT NULL DEFAULT COALESCE(current_setting('adtr.audit_request_id',true),''),
 occurred_at timestamptz NOT NULL,
 UNIQUE(operation_id,task_id),
 CHECK(archived=(action='archive'))
);
CREATE INDEX task_archive_events_task ON adtr.task_archive_events(tenant_id,task_id,id);
CREATE TRIGGER task_archive_events_immutable BEFORE UPDATE OR DELETE ON adtr.task_archive_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_archive_events_no_truncate BEFORE TRUNCATE ON adtr.task_archive_events FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
`
