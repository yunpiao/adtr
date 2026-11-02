package simulationdetectionschedules

// SchemaSQL is installed only by explicit migration 27. No existing health or
// manual-detection constraint is broadened. No grants are inserted.
const SchemaSQL = `
CREATE TABLE adtr.simulation_detection_schedules (
 tenant_id text NOT NULL, domain_id text NOT NULL CHECK(domain_id<>'platform'), actor_id bigint NOT NULL CHECK(actor_id>0),
 schedule_id text NOT NULL, revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 1024),
 control_version bigint NOT NULL CHECK(control_version>0), authorization_version text NOT NULL CHECK(authorization_version<>''),
 state text NOT NULL CHECK(state IN ('paused','enabled','archived','authorization_blocked','configuration_blocked')),
 error_code text NOT NULL DEFAULT '' CHECK(error_code IN ('','authorization_revoked','configuration_changed','schedule_time_overflow')),
 next_index bigint NOT NULL CHECK(next_index>=0), next_at timestamptz NOT NULL CHECK(isfinite(next_at)),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,domain_id,actor_id,schedule_id),UNIQUE(schedule_id));
CREATE INDEX simulation_detection_schedule_due ON adtr.simulation_detection_schedules(next_at,schedule_id) WHERE state='enabled';
CREATE TABLE adtr.simulation_detection_schedule_revisions (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL,schedule_id text NOT NULL,revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 1024),
 definition jsonb NOT NULL CHECK(jsonb_typeof(definition)='object'),payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 anchor timestamptz NOT NULL CHECK(isfinite(anchor) AND anchor=date_trunc('second',anchor)),
 PRIMARY KEY(tenant_id,domain_id,actor_id,schedule_id,revision),
 FOREIGN KEY(tenant_id,domain_id,actor_id,schedule_id) REFERENCES adtr.simulation_detection_schedules(tenant_id,domain_id,actor_id,schedule_id));
CREATE TABLE adtr.simulation_detection_schedule_operations (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL,operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 PRIMARY KEY(tenant_id,domain_id,actor_id,operation_id));
CREATE TABLE adtr.simulation_detection_schedule_events (
 id bigserial PRIMARY KEY,tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL,schedule_id text NOT NULL,revision bigint NOT NULL,
 action text NOT NULL CHECK(action IN ('created','edited','enabled','paused','archived','authorization_blocked','configuration_blocked','admitted','skipped_misfire','skipped_overlap','skipped_paused','schedule_time_overflow')),
 first_index bigint,last_index bigint,task_id text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,domain_id,actor_id,schedule_id,revision) REFERENCES adtr.simulation_detection_schedule_revisions(tenant_id,domain_id,actor_id,schedule_id,revision),
 CHECK((first_index IS NULL AND last_index IS NULL) OR (first_index IS NOT NULL AND last_index IS NOT NULL AND first_index>=0 AND last_index>=first_index)),
 CHECK((action IN ('admitted','skipped_misfire','skipped_overlap','skipped_paused'))=(first_index IS NOT NULL)),
 CHECK((action='admitted')=(task_id<>'')));
CREATE INDEX simulation_detection_schedule_history ON adtr.simulation_detection_schedule_events(tenant_id,domain_id,actor_id,schedule_id,id);
CREATE TABLE adtr.simulation_detection_schedule_intents (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL,schedule_id text NOT NULL,revision bigint NOT NULL,
 scheduled_at timestamptz NOT NULL CHECK(isfinite(scheduled_at)),task_id text NOT NULL UNIQUE,payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
 PRIMARY KEY(tenant_id,schedule_id,scheduled_at),
 FOREIGN KEY(tenant_id,domain_id,actor_id,schedule_id,revision) REFERENCES adtr.simulation_detection_schedule_revisions(tenant_id,domain_id,actor_id,schedule_id,revision),
 FOREIGN KEY(task_id) REFERENCES adtr.tasks(task_id));
CREATE FUNCTION adtr.check_simulation_schedule_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'schedule cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.domain_id,NEW.actor_id,NEW.schedule_id,NEW.authorization_version,NEW.created_at)
 IS DISTINCT FROM (OLD.tenant_id,OLD.domain_id,OLD.actor_id,OLD.schedule_id,OLD.authorization_version,OLD.created_at)
 OR NEW.revision<OLD.revision OR NEW.revision>OLD.revision+1 OR NEW.control_version<OLD.control_version
 OR (NEW.revision=OLD.revision AND NEW.next_index<OLD.next_index)
 OR (OLD.state IN ('archived','authorization_blocked') AND NEW.state<>OLD.state)
 THEN RAISE EXCEPTION 'invalid schedule transition'; END IF; RETURN NEW; END $$;
CREATE TRIGGER simulation_schedule_updates BEFORE UPDATE OR DELETE ON adtr.simulation_detection_schedules FOR EACH ROW EXECUTE FUNCTION adtr.check_simulation_schedule_update();
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_detection_schedule_revisions','simulation_detection_schedule_operations','simulation_detection_schedule_events','simulation_detection_schedule_intents'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_schedule_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['simulation_detection_schedules','simulation_detection_schedule_revisions','simulation_detection_schedule_operations','simulation_detection_schedule_events','simulation_detection_schedule_intents'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_schedule_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP; END $$;
`
