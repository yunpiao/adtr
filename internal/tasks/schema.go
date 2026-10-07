package tasks

// Schema runs only through the explicit versioned migrator. History and
// idempotency records deliberately have no implicit retention/deletion policy.
const Schema = queueSchema + ScheduleSchema

const queueSchema = `
CREATE TABLE adtr.tasks (
 task_id text PRIMARY KEY, tenant_id text NOT NULL, domain_id text NOT NULL,
 kind text NOT NULL, payload_version integer NOT NULL CHECK(payload_version>0),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'), payload_hash text NOT NULL,
 actor_id bigint NOT NULL, authorization_version text NOT NULL, idempotency_key text NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','running','retry_wait','cancel_requested','succeeded','failed','partial_failed','dead_letter','cancelled')),
 attempt integer NOT NULL DEFAULT 0 CHECK(attempt>=0), max_attempts integer NOT NULL CHECK(max_attempts BETWEEN 1 AND 5),
 lease_owner text NOT NULL DEFAULT '', lease_until timestamptz, fencing_token bigint NOT NULL DEFAULT 0 CHECK(fencing_token>=0),
 cursor jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(cursor)='object'), result jsonb NOT NULL DEFAULT '{}' CHECK(jsonb_typeof(result)='object'),
 progress integer NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100), result_version bigint NOT NULL DEFAULT 0 CHECK(result_version>=0),
 error_code text NOT NULL DEFAULT '', parent_task_id text NOT NULL DEFAULT '',
 next_attempt_at timestamptz, created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,domain_id,kind,idempotency_key), CHECK(attempt<=max_attempts),
 CHECK((lease_owner='' AND lease_until IS NULL) OR (lease_owner<>'' AND lease_until IS NOT NULL))
);
CREATE FUNCTION adtr.reject_task_admission_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.task_id,NEW.tenant_id,NEW.domain_id,NEW.kind,NEW.payload_version,NEW.payload,NEW.payload_hash,NEW.actor_id,NEW.authorization_version,NEW.idempotency_key,NEW.max_attempts,NEW.parent_task_id,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.task_id,OLD.tenant_id,OLD.domain_id,OLD.kind,OLD.payload_version,OLD.payload,OLD.payload_hash,OLD.actor_id,OLD.authorization_version,OLD.idempotency_key,OLD.max_attempts,OLD.parent_task_id,OLD.created_at) THEN
  RAISE EXCEPTION 'task admission identity is immutable' USING ERRCODE='42501';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER tasks_admission_immutable BEFORE UPDATE ON adtr.tasks FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_admission_mutation();
CREATE INDEX tasks_ready ON adtr.tasks(state,next_attempt_at,created_at,task_id);
CREATE INDEX tasks_scope ON adtr.tasks(tenant_id,domain_id,created_at,task_id);
CREATE INDEX tasks_leases ON adtr.tasks(lease_until) WHERE lease_until IS NOT NULL;
CREATE TABLE adtr.task_events (
 id bigserial PRIMARY KEY, task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 tenant_id text NOT NULL, domain_id text NOT NULL, actor_id bigint NOT NULL,
 action text NOT NULL, state text NOT NULL, attempt integer NOT NULL,
 result_version bigint NOT NULL, fencing_token bigint NOT NULL,
 authorization_version text NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX task_events_task ON adtr.task_events(task_id,id);
CREATE TABLE adtr.task_outbox (
 id bigserial PRIMARY KEY, task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 event_id bigint NOT NULL UNIQUE REFERENCES adtr.task_events(id),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE OR REPLACE FUNCTION adtr.reject_task_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'task history is append-only'; END;
$$;
CREATE TRIGGER task_events_immutable BEFORE UPDATE OR DELETE ON adtr.task_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_events_no_truncate BEFORE TRUNCATE ON adtr.task_events FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_outbox_immutable BEFORE UPDATE OR DELETE ON adtr.task_outbox FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_outbox_no_truncate BEFORE TRUNCATE ON adtr.task_outbox FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
`

// ScheduleSchema is included by Schema through the migrator alongside the queue.
const ScheduleSchema = `
CREATE TABLE adtr.task_schedule_occurrences (
 tenant_id text NOT NULL, schedule_id text NOT NULL, scheduled_at timestamptz NOT NULL,
 task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 PRIMARY KEY(tenant_id,schedule_id,scheduled_at)
);
CREATE TRIGGER task_schedule_immutable BEFORE UPDATE OR DELETE ON adtr.task_schedule_occurrences FOR EACH ROW EXECUTE FUNCTION adtr.reject_task_history_mutation();
CREATE TRIGGER task_schedule_no_truncate BEFORE TRUNCATE ON adtr.task_schedule_occurrences FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_task_history_mutation();
`
