package schedules

// Schema belongs to the explicit version-8 migrator, never API startup.
const Schema = `
CREATE TABLE adtr.task_schedules (
 schedule_id text PRIMARY KEY, tenant_id text NOT NULL, actor_id bigint NOT NULL,
 authorization_version text NOT NULL CHECK(authorization_version<>''),
 label text NOT NULL, kind text NOT NULL CHECK(kind='infrastructure.health'),
 domain_id text NOT NULL CHECK(domain_id='platform'), payload_version integer NOT NULL CHECK(payload_version=1),
 payload jsonb NOT NULL CHECK(payload='{}'::jsonb),
 start_at timestamptz NOT NULL CHECK(isfinite(start_at) AND start_at=date_trunc('second',start_at)),
 interval_seconds bigint NOT NULL CHECK(interval_seconds BETWEEN 60 AND 86400),
 definition_hash text NOT NULL, creation_key text NOT NULL,
 state text NOT NULL DEFAULT 'paused' CHECK(state IN ('paused','enabled','authorization_blocked')),
 control_version bigint NOT NULL DEFAULT 1 CHECK(control_version>0),
 next_index bigint NOT NULL DEFAULT 0 CHECK(next_index>=0), next_at timestamptz NOT NULL CHECK(isfinite(next_at)),
 last_task_id text NOT NULL DEFAULT '', last_scheduled_at timestamptz,
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','authorization_revoked','schedule_time_overflow')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,actor_id,creation_key),
 CHECK(extract(epoch FROM (next_at-start_at))=interval_seconds::numeric*next_index)
);
CREATE INDEX task_schedules_due ON adtr.task_schedules(next_at,schedule_id) WHERE state='enabled';
CREATE INDEX task_schedules_owner ON adtr.task_schedules(tenant_id,actor_id,created_at,schedule_id);
CREATE FUNCTION adtr.reject_schedule_definition_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.schedule_id,NEW.tenant_id,NEW.actor_id,NEW.authorization_version,NEW.label,NEW.kind,NEW.domain_id,NEW.payload_version,NEW.payload,NEW.start_at,NEW.interval_seconds,NEW.definition_hash,NEW.creation_key,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.schedule_id,OLD.tenant_id,OLD.actor_id,OLD.authorization_version,OLD.label,OLD.kind,OLD.domain_id,OLD.payload_version,OLD.payload,OLD.start_at,OLD.interval_seconds,OLD.definition_hash,OLD.creation_key,OLD.created_at) THEN
  RAISE EXCEPTION 'schedule definition is immutable' USING ERRCODE='42501';
 END IF;
 IF NEW.next_index<OLD.next_index OR NEW.control_version<OLD.control_version OR
 (OLD.state='authorization_blocked' AND NEW.state<>'authorization_blocked') THEN
  RAISE EXCEPTION 'schedule cursor and authorization cannot rewind' USING ERRCODE='42501';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER task_schedules_definition_immutable BEFORE UPDATE ON adtr.task_schedules FOR EACH ROW EXECUTE FUNCTION adtr.reject_schedule_definition_mutation();
CREATE TRIGGER task_schedules_no_delete BEFORE DELETE ON adtr.task_schedules FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_schedules_no_truncate BEFORE TRUNCATE ON adtr.task_schedules FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TABLE adtr.task_schedule_events (
 id bigserial PRIMARY KEY, schedule_id text NOT NULL REFERENCES adtr.task_schedules(schedule_id),
 tenant_id text NOT NULL, actor_id bigint NOT NULL, authorization_version text NOT NULL,
 action text NOT NULL CHECK(action IN ('created','enabled','paused','authorization_blocked','admitted','skipped_paused','skipped_misfire','skipped_overlap','schedule_time_overflow')),
 first_index bigint, last_index bigint, task_id text NOT NULL DEFAULT '', control_version bigint NOT NULL,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK((first_index IS NULL AND last_index IS NULL) OR (first_index IS NOT NULL AND last_index IS NOT NULL AND first_index>=0 AND last_index>=first_index)),
 CHECK((action IN ('admitted','skipped_paused','skipped_misfire','skipped_overlap') AND first_index IS NOT NULL) OR (action NOT IN ('admitted','skipped_paused','skipped_misfire','skipped_overlap') AND first_index IS NULL)),
 CHECK((action='admitted' AND task_id<>'' AND first_index=last_index) OR (action<>'admitted' AND task_id=''))
);
CREATE INDEX task_schedule_events_owner ON adtr.task_schedule_events(tenant_id,schedule_id,id);
CREATE TRIGGER task_schedule_events_immutable BEFORE UPDATE OR DELETE ON adtr.task_schedule_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_schedule_events_no_truncate BEFORE TRUNCATE ON adtr.task_schedule_events FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TABLE adtr.task_schedule_operations (
 operation_id text PRIMARY KEY, tenant_id text NOT NULL, actor_id bigint NOT NULL,
 operation_key text NOT NULL, request_hash text NOT NULL,
 schedule_id text NOT NULL REFERENCES adtr.task_schedules(schedule_id),
 action text NOT NULL CHECK(action IN ('enable','pause')), state text NOT NULL CHECK(state IN ('paused','enabled')),
 control_version bigint NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,actor_id,operation_key)
);
CREATE TRIGGER task_schedule_operations_immutable BEFORE UPDATE OR DELETE ON adtr.task_schedule_operations FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_schedule_operations_no_truncate BEFORE TRUNCATE ON adtr.task_schedule_operations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
`
