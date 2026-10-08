package operationallogs

// BundleSchema follows Schema. Staging is immutable and fence-specific; only a
// successful task terminal state publishes an artifact. There is no file path.
const BundleSchema = `
CREATE TABLE adtr.operational_log_bundle_snapshots (
 task_id text NOT NULL REFERENCES adtr.tasks(task_id), fencing_token bigint NOT NULL CHECK(fencing_token>0),
 tenant_id text NOT NULL CHECK(tenant_id='default'),actor_id bigint NOT NULL CHECK(actor_id>0),authorization_version text NOT NULL,
 captured_at timestamptz NOT NULL DEFAULT clock_timestamp(),row_count integer NOT NULL,
 selection jsonb NOT NULL CHECK(jsonb_typeof(selection)='object' AND octet_length(selection::text)<=4096),
 coverage jsonb NOT NULL CHECK(jsonb_typeof(coverage)='object' AND octet_length(coverage::text)<=4096),
 PRIMARY KEY(task_id,fencing_token),CONSTRAINT operational_log_bundle_row_limit CHECK(row_count BETWEEN 0 AND 10000)
);
CREATE TABLE adtr.operational_log_bundle_rows (
 task_id text NOT NULL,fencing_token bigint NOT NULL,ordinal integer NOT NULL CHECK(ordinal BETWEEN 1 AND 10000),
 event_id text NOT NULL REFERENCES adtr.operational_log_events(event_id),
 event_data jsonb NOT NULL CHECK(jsonb_typeof(event_data)='object' AND octet_length(event_data::text)<=4096),
 PRIMARY KEY(task_id,fencing_token,ordinal),UNIQUE(task_id,fencing_token,event_id),
 FOREIGN KEY(task_id,fencing_token) REFERENCES adtr.operational_log_bundle_snapshots(task_id,fencing_token)
);
CREATE TABLE adtr.operational_log_bundle_chunks (
 task_id text NOT NULL,fencing_token bigint NOT NULL,chunk_no integer NOT NULL CHECK(chunk_no BETWEEN 0 AND 15),
 data bytea NOT NULL CHECK(octet_length(data) BETWEEN 1 AND 1048576),sha256 text NOT NULL CHECK(sha256~'^[0-9a-f]{64}$'),
 PRIMARY KEY(task_id,fencing_token,chunk_no),
 FOREIGN KEY(task_id,fencing_token) REFERENCES adtr.operational_log_bundle_snapshots(task_id,fencing_token)
);
CREATE TABLE adtr.operational_log_bundle_manifests (
 task_id text NOT NULL,fencing_token bigint NOT NULL,
 byte_count bigint NOT NULL CHECK(byte_count BETWEEN 1 AND 16777216),chunk_count integer NOT NULL CHECK(chunk_count BETWEEN 1 AND 16),
 row_count integer NOT NULL CHECK(row_count BETWEEN 0 AND 10000),sha256 text NOT NULL CHECK(sha256~'^[0-9a-f]{64}$'),
 jsonl_sha256 text NOT NULL CHECK(jsonl_sha256~'^[0-9a-f]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=4096),
 PRIMARY KEY(task_id,fencing_token),
 FOREIGN KEY(task_id,fencing_token) REFERENCES adtr.operational_log_bundle_snapshots(task_id,fencing_token)
);
CREATE TRIGGER operational_log_bundle_snapshots_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_bundle_snapshots FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_snapshots_no_truncate BEFORE TRUNCATE ON adtr.operational_log_bundle_snapshots FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_rows_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_bundle_rows FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_rows_no_truncate BEFORE TRUNCATE ON adtr.operational_log_bundle_rows FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_chunks_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_bundle_chunks FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_chunks_no_truncate BEFORE TRUNCATE ON adtr.operational_log_bundle_chunks FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_manifests_immutable BEFORE UPDATE OR DELETE ON adtr.operational_log_bundle_manifests FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operational_log_bundle_manifests_no_truncate BEFORE TRUNCATE ON adtr.operational_log_bundle_manifests FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
