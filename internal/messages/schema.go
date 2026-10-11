package messages

// Schema is installed atomically by the shared explicit migrator. It does not
// mutate task producers, their immutable outbox, or the imported pure module.
const Schema = `
CREATE TABLE adtr.messages (
 message_id text PRIMARY KEY,
 tenant_id text NOT NULL, recipient_user_id bigint NOT NULL,
 source_task_id text NOT NULL, source_event_id bigint NOT NULL UNIQUE REFERENCES adtr.task_events(id),
 source_result_version bigint NOT NULL,
 message_type text NOT NULL CHECK(message_type='task_result'),
 sub_type text NOT NULL CHECK(sub_type IN ('succeeded','failed','partial_failed','dead_letter','cancelled')),
 title text NOT NULL, description text NOT NULL,
 occurred_at timestamptz NOT NULL, ingested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,source_task_id,source_result_version,recipient_user_id),
 UNIQUE(tenant_id,recipient_user_id,message_id),
 FOREIGN KEY(tenant_id,source_task_id) REFERENCES adtr.tasks(tenant_id,task_id)
);
CREATE INDEX messages_recipient_time ON adtr.messages(tenant_id,recipient_user_id,occurred_at,message_id COLLATE "C");
CREATE TABLE adtr.message_reads (
 tenant_id text NOT NULL,user_id bigint NOT NULL,message_id text NOT NULL,
 first_read_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,user_id,message_id),
 FOREIGN KEY(tenant_id,user_id,message_id) REFERENCES adtr.messages(tenant_id,recipient_user_id,message_id)
);
CREATE TABLE adtr.message_results (
 result_id text PRIMARY KEY,tenant_id text NOT NULL,user_id bigint NOT NULL,
 session_hash text NOT NULL,authorization_version bigint NOT NULL CHECK(authorization_version>0),
 projection_version integer NOT NULL CHECK(projection_version=1),
 confirmed_at timestamptz NOT NULL,expires_at timestamptz NOT NULL CHECK(expires_at>confirmed_at),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=4194304)
);
CREATE INDEX message_results_expiry ON adtr.message_results(expires_at,result_id);
CREATE INDEX message_results_user_expiry ON adtr.message_results(tenant_id,user_id,expires_at);
CREATE INDEX message_results_tenant_expiry ON adtr.message_results(tenant_id,expires_at);
CREATE TABLE adtr.message_read_operations (
 tenant_id text NOT NULL,user_id bigint NOT NULL,operation_id text NOT NULL,
 result_id text NOT NULL,message_id text NOT NULL,
 first_read_at timestamptz NOT NULL,committed_at timestamptz NOT NULL,
 newly_read boolean NOT NULL,
 PRIMARY KEY(tenant_id,user_id,operation_id),
 FOREIGN KEY(tenant_id,user_id,message_id) REFERENCES adtr.messages(tenant_id,recipient_user_id,message_id)
);
-- No result FK: an unknown committed outcome must remain confirmable after
-- snapshot expiry, cleanup, logout, or a new authenticated session.
CREATE TABLE adtr.message_projection_cursor (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 after_id bigint NOT NULL DEFAULT 0 CHECK(after_id>=0),
 upper_id bigint NOT NULL DEFAULT 0 CHECK(upper_id>=after_id)
);
INSERT INTO adtr.message_projection_cursor(singleton) VALUES(true);
CREATE FUNCTION adtr.reject_message_history_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'message history is append-only'; END;
$$;
CREATE TRIGGER messages_immutable BEFORE UPDATE OR DELETE ON adtr.messages FOR EACH ROW EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER messages_no_truncate BEFORE TRUNCATE ON adtr.messages FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_reads_immutable BEFORE UPDATE OR DELETE ON adtr.message_reads FOR EACH ROW EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_reads_no_truncate BEFORE TRUNCATE ON adtr.message_reads FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_read_operations_immutable BEFORE UPDATE OR DELETE ON adtr.message_read_operations FOR EACH ROW EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_read_operations_no_truncate BEFORE TRUNCATE ON adtr.message_read_operations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_message_history_mutation();
`
