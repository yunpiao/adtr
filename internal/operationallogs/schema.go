package operationallogs

// Schema is applied only by migration 14. There is deliberately no deletion or
// automatic retention path. Report rows are incomplete cumulative observations.
const Schema = `
CREATE TABLE adtr.operational_log_state (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO adtr.operational_log_state(singleton) VALUES(true);
CREATE TABLE adtr.operational_log_events (
 event_id text PRIMARY KEY CHECK(event_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 process_id text NOT NULL CHECK(process_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 schema_version integer NOT NULL CHECK(schema_version=1),
 module text NOT NULL CHECK(module IN ('api','worker')),
 code text NOT NULL,outcome text NOT NULL,severity text NOT NULL,reason text NOT NULL DEFAULT '',
 observed_at timestamptz NOT NULL CHECK(isfinite(observed_at)),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(recorded_at)),
 CHECK((severity='error' AND outcome='failed') OR (severity='info' AND outcome IN ('attempted','completed','progress'))),
 CHECK((code='service_start_requested' AND outcome='attempted' AND reason='') OR
 (code='service_stopped' AND outcome='completed' AND reason='') OR
 (code='service_failed' AND outcome='failed' AND ((module='api' AND reason='serve_failed') OR (module='worker' AND reason='worker_failed'))) OR
 (module='worker' AND code IN ('queue_cycle','recovery_cycle','scheduler_cycle') AND ((outcome='completed' AND reason='') OR (outcome='failed' AND reason='cycle_failed'))) OR
 (module='worker' AND code='queue_progress' AND outcome='progress' AND reason=''))
);
CREATE INDEX operational_log_events_time ON adtr.operational_log_events(recorded_at DESC,event_id COLLATE "C" DESC);
CREATE INDEX operational_log_events_module_time ON adtr.operational_log_events(module,recorded_at DESC,event_id COLLATE "C" DESC);
CREATE TABLE adtr.operational_log_reports (
 process_id text PRIMARY KEY CHECK(process_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
 module text NOT NULL CHECK(module IN ('api','worker')),
 started_at timestamptz NOT NULL CHECK(isfinite(started_at)),
 observed_at timestamptz NOT NULL CHECK(isfinite(observed_at)),
 reported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 accepted bigint NOT NULL CHECK(accepted>=0),acknowledged bigint NOT NULL CHECK(acknowledged>=0),
 rejected bigint NOT NULL CHECK(rejected>=0),queue_full bigint NOT NULL CHECK(queue_full>=0),
 write_failures bigint NOT NULL CHECK(write_failures>=0),unacknowledged bigint NOT NULL CHECK(unacknowledged>=0),
 abandoned bigint NOT NULL CHECK(abandoned>=0),acceptance_stopped boolean NOT NULL,
 CHECK(acknowledged<=accepted AND unacknowledged<=accepted AND abandoned<=accepted),
 CHECK(acknowledged::numeric+unacknowledged::numeric+abandoned::numeric<=accepted::numeric)
);
CREATE INDEX operational_log_reports_time ON adtr.operational_log_reports(reported_at DESC,process_id COLLATE "C");
CREATE FUNCTION adtr.guard_operational_log_report() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'operational log reports cannot be removed' USING ERRCODE='42501'; END IF;
 IF ROW(NEW.process_id,NEW.module,NEW.started_at) IS DISTINCT FROM ROW(OLD.process_id,OLD.module,OLD.started_at)
 OR NEW.observed_at<OLD.observed_at OR NEW.reported_at<OLD.reported_at OR NEW.accepted<OLD.accepted
 OR NEW.acknowledged<OLD.acknowledged OR NEW.rejected<OLD.rejected OR NEW.queue_full<OLD.queue_full
 OR NEW.write_failures<OLD.write_failures OR NEW.unacknowledged<OLD.unacknowledged OR NEW.abandoned<OLD.abandoned
 OR (OLD.acceptance_stopped AND NOT NEW.acceptance_stopped) THEN
 RAISE EXCEPTION 'operational log reports only advance' USING ERRCODE='42501'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER operational_log_reports_monotone BEFORE UPDATE OR DELETE ON adtr.operational_log_reports FOR EACH ROW EXECUTE FUNCTION adtr.guard_operational_log_report();
CREATE TRIGGER operational_log_reports_no_truncate BEFORE TRUNCATE ON adtr.operational_log_reports FOR EACH STATEMENT EXECUTE FUNCTION adtr.guard_operational_log_report();
CREATE FUNCTION adtr.reject_operational_log_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'operational logs are append-only' USING ERRCODE='42501'; END; $$;
CREATE TRIGGER operational_log_state_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_state FOR EACH ROW EXECUTE FUNCTION adtr.reject_operational_log_mutation();
CREATE TRIGGER operational_log_state_no_truncate BEFORE TRUNCATE ON adtr.operational_log_state FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_operational_log_mutation();
CREATE TRIGGER operational_log_events_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_events FOR EACH ROW EXECUTE FUNCTION adtr.reject_operational_log_mutation();
CREATE TRIGGER operational_log_events_no_truncate BEFORE TRUNCATE ON adtr.operational_log_events FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_operational_log_mutation();
CREATE TABLE adtr.operational_log_audit (
 id bigserial PRIMARY KEY,tenant_id text NOT NULL CHECK(tenant_id='default'),actor_id bigint NOT NULL CHECK(actor_id>0),
 task_id text NOT NULL REFERENCES adtr.tasks(task_id),action text NOT NULL CHECK(action IN ('bundle_submit','bundle_cancel')),
 audit_username text,audit_ip text,audit_path text,audit_request_id text,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),UNIQUE(task_id,action)
);
CREATE FUNCTION adtr.guard_operational_log_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT FROM adtr.tasks t WHERE t.task_id=NEW.task_id AND t.tenant_id=NEW.tenant_id AND t.actor_id=NEW.actor_id AND t.domain_id='platform' AND t.kind='system.logs_bundle'
  AND ((NEW.action='bundle_submit' AND t.state='queued') OR (NEW.action='bundle_cancel' AND t.state IN ('cancel_requested','cancelled')))) THEN
 RAISE EXCEPTION 'operational log control identity invalid' USING ERRCODE='42501'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER operational_log_audit_guard BEFORE INSERT ON adtr.operational_log_audit FOR EACH ROW EXECUTE FUNCTION adtr.guard_operational_log_audit();
CREATE INDEX operational_log_audit_time ON adtr.operational_log_audit(tenant_id,occurred_at,id);
CREATE TRIGGER operational_log_audit_capture BEFORE INSERT ON adtr.operational_log_audit FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
CREATE TRIGGER operational_log_audit_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_operational_log_mutation();
CREATE TRIGGER operational_log_audit_no_truncate BEFORE TRUNCATE ON adtr.operational_log_audit FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_operational_log_mutation();
`
