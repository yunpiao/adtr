package systemhealth

// Schema is installed only by the explicit schema-7 migrator. Observations and
// audit records have no implicit retention or cleanup policy.
const Schema = `
CREATE TABLE adtr.system_resource_samples (
 tenant_id text NOT NULL, instance_id text NOT NULL,
 observed_at timestamptz NOT NULL,
 cpu_percent double precision CHECK(cpu_percent BETWEEN 0 AND 100),
 ram_percent double precision CHECK(ram_percent BETWEEN 0 AND 100),
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object'),
 PRIMARY KEY(tenant_id,instance_id,observed_at)
);
CREATE TABLE adtr.system_storage_samples (
 tenant_id text NOT NULL, instance_id text NOT NULL, mount_id text NOT NULL,
 observed_at timestamptz NOT NULL,
 use_percent double precision CHECK(use_percent BETWEEN 0 AND 100),
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object'),
 PRIMARY KEY(tenant_id,instance_id,mount_id,observed_at),
 FOREIGN KEY(tenant_id,instance_id,observed_at)
 REFERENCES adtr.system_resource_samples(tenant_id,instance_id,observed_at)
);
CREATE TABLE adtr.system_alarm_settings (
 tenant_id text NOT NULL, instance_id text NOT NULL, mount_id text NOT NULL,
 alarm_percent integer NOT NULL DEFAULT 85 CHECK(alarm_percent BETWEEN 85 AND 90),
 revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0),
 updated_at timestamptz,
 PRIMARY KEY(tenant_id,instance_id,mount_id)
);
CREATE TABLE adtr.system_alarm_audit (
 id bigserial PRIMARY KEY, tenant_id text NOT NULL, instance_id text NOT NULL,
 mount_id text NOT NULL, actor_id bigint NOT NULL CHECK(actor_id>0),
 previous_percent integer NOT NULL CHECK(previous_percent BETWEEN 85 AND 90),
 alarm_percent integer NOT NULL CHECK(alarm_percent BETWEEN 85 AND 90),
 previous_revision bigint NOT NULL CHECK(previous_revision>=0),
 revision bigint NOT NULL CHECK(revision=previous_revision+1),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,instance_id,mount_id,revision)
);
CREATE FUNCTION adtr.reject_system_alarm_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'system alarm audit is append-only' USING ERRCODE='42501'; END;
$$;
CREATE TRIGGER system_alarm_audit_immutable BEFORE UPDATE OR DELETE ON adtr.system_alarm_audit
 FOR EACH ROW EXECUTE FUNCTION adtr.reject_system_alarm_audit_mutation();
CREATE TRIGGER system_alarm_audit_no_truncate BEFORE TRUNCATE ON adtr.system_alarm_audit
 FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_system_alarm_audit_mutation();
CREATE TABLE adtr.system_worker_activity (
 tenant_id text NOT NULL, worker_id text NOT NULL, cycle text NOT NULL CHECK(cycle IN ('queue','recovery')),
 status text NOT NULL CHECK(status IN ('success','failure','progress')),
 error_code text NOT NULL DEFAULT '',
 last_activity_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_success_at timestamptz,
 PRIMARY KEY(tenant_id,worker_id,cycle),
 CHECK(status<>'progress' OR cycle='queue'),
 CHECK((status='failure' AND error_code IN ('cycle_failed','database_unavailable','execution_failed','schema_incompatible'))
    OR (status IN ('success','progress') AND error_code=''))
);
CREATE INDEX system_worker_activity_recent ON adtr.system_worker_activity(tenant_id,last_activity_at DESC);
`

// SchedulerActivitySchema extends real worker observation in migration 8.
const SchedulerActivitySchema = `
ALTER TABLE adtr.system_worker_activity DROP CONSTRAINT system_worker_activity_cycle_check;
ALTER TABLE adtr.system_worker_activity ADD CONSTRAINT system_worker_activity_cycle_check CHECK(cycle IN ('queue','recovery','scheduler'));
`
