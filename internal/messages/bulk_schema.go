package messages

// BulkSchema preserves original single-message receipts and frozen snapshots.
const BulkSchema = `
CREATE TABLE adtr.message_bulk_operations (
 tenant_id text NOT NULL,user_id bigint NOT NULL,operation_id text NOT NULL,
 queue_id bigserial UNIQUE NOT NULL,result_id text NOT NULL,selection text NOT NULL CHECK(selection IN ('selected','all')),
 request_hash text NOT NULL CHECK(length(request_hash)=64),authorization_version bigint NOT NULL CHECK(authorization_version>0),
 accepted_at timestamptz NOT NULL,updated_at timestamptz NOT NULL,
 state text NOT NULL CHECK(state IN ('queued','running','completed','cancelled','authorization_blocked')),
 target integer NOT NULL CHECK(target BETWEEN 0 AND 1000),processed integer NOT NULL DEFAULT 0 CHECK(processed>=0),
 newly_read integer NOT NULL DEFAULT 0 CHECK(newly_read>=0),already_read integer NOT NULL DEFAULT 0 CHECK(already_read>=0),
 PRIMARY KEY(tenant_id,user_id,operation_id),
 CHECK(processed=newly_read+already_read AND processed<=target),
 CHECK((state='completed')=(processed=target)),
 CHECK(state<>'queued' OR processed=0)
);
CREATE INDEX message_bulk_active_queue ON adtr.message_bulk_operations(queue_id) WHERE state IN ('queued','running');
CREATE INDEX message_bulk_active_actor ON adtr.message_bulk_operations(tenant_id,user_id) WHERE state IN ('queued','running');
CREATE TABLE adtr.message_bulk_targets (
 tenant_id text NOT NULL,user_id bigint NOT NULL,operation_id text NOT NULL,ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 999),message_id text NOT NULL,
 PRIMARY KEY(tenant_id,user_id,operation_id,ordinal),
 UNIQUE(tenant_id,user_id,operation_id,message_id),
 FOREIGN KEY(tenant_id,user_id,operation_id) REFERENCES adtr.message_bulk_operations(tenant_id,user_id,operation_id),
 FOREIGN KEY(tenant_id,user_id,message_id) REFERENCES adtr.messages(tenant_id,recipient_user_id,message_id)
);
CREATE TABLE adtr.message_bulk_receipts (
 tenant_id text NOT NULL,user_id bigint NOT NULL,operation_id text NOT NULL,ordinal integer NOT NULL,
 first_read_at timestamptz NOT NULL,committed_at timestamptz NOT NULL,newly_read boolean NOT NULL,
 PRIMARY KEY(tenant_id,user_id,operation_id,ordinal),
 FOREIGN KEY(tenant_id,user_id,operation_id,ordinal) REFERENCES adtr.message_bulk_targets(tenant_id,user_id,operation_id,ordinal)
);
CREATE TRIGGER message_bulk_targets_immutable BEFORE UPDATE OR DELETE ON adtr.message_bulk_targets FOR EACH ROW EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_bulk_targets_no_truncate BEFORE TRUNCATE ON adtr.message_bulk_targets FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_bulk_receipts_immutable BEFORE UPDATE OR DELETE ON adtr.message_bulk_receipts FOR EACH ROW EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TRIGGER message_bulk_receipts_no_truncate BEFORE TRUNCATE ON adtr.message_bulk_receipts FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE FUNCTION adtr.guard_message_bulk_operation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP <> 'UPDATE' THEN RAISE EXCEPTION 'bulk operations retained'; END IF;
 IF (NEW.tenant_id,NEW.user_id,NEW.operation_id,NEW.queue_id,NEW.result_id,NEW.selection,NEW.request_hash,NEW.authorization_version,NEW.accepted_at,NEW.target)
 IS DISTINCT FROM (OLD.tenant_id,OLD.user_id,OLD.operation_id,OLD.queue_id,OLD.result_id,OLD.selection,OLD.request_hash,OLD.authorization_version,OLD.accepted_at,OLD.target)
 OR OLD.state IN ('completed','cancelled','authorization_blocked')
 OR NEW.processed<OLD.processed OR NEW.newly_read<OLD.newly_read OR NEW.already_read<OLD.already_read
 THEN RAISE EXCEPTION 'bulk receipt or terminal state immutable'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER message_bulk_operations_guard BEFORE UPDATE OR DELETE ON adtr.message_bulk_operations FOR EACH ROW EXECUTE FUNCTION adtr.guard_message_bulk_operation();
CREATE TRIGGER message_bulk_operations_no_truncate BEFORE TRUNCATE ON adtr.message_bulk_operations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_message_history_mutation();
CREATE TABLE adtr.message_bulk_cursor(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),after_id bigint NOT NULL DEFAULT 0 CHECK(after_id>=0),upper_id bigint NOT NULL DEFAULT 0 CHECK(upper_id>=after_id));
INSERT INTO adtr.message_bulk_cursor(singleton) VALUES(true);
`
