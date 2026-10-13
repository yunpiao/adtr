package domains

// Migration21 stores generated artifacts separately from immutable observations.
// Cleanup never deletes source observations, task identity, receipts or audit.
const UserAssetExportV3Schema = `
CREATE TABLE adtr.user_asset_export_snapshots (
 task_id text PRIMARY KEY REFERENCES adtr.tasks(task_id), tenant_id text NOT NULL,
 actor_id bigint NOT NULL, authorization_version text NOT NULL,
 observation_id text NOT NULL, source_sha256 text NOT NULL CHECK(source_sha256 ~ '^[a-f0-9]{64}$'),
 payload_hash text NOT NULL, row_count integer NOT NULL CHECK(row_count BETWEEN 0 AND 10000),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE adtr.user_asset_export_chunks (
 task_id text NOT NULL REFERENCES adtr.user_asset_export_snapshots(task_id),
 fencing_token bigint NOT NULL CHECK(fencing_token>0), chunk_no integer NOT NULL CHECK(chunk_no BETWEEN 0 AND 31),
 data bytea NOT NULL CHECK(octet_length(data) BETWEEN 1 AND 1048576),
 sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'), PRIMARY KEY(task_id,fencing_token,chunk_no)
);
CREATE TABLE adtr.user_asset_export_manifests (
 task_id text NOT NULL REFERENCES adtr.user_asset_export_snapshots(task_id),
 fencing_token bigint NOT NULL CHECK(fencing_token>0),
 byte_count bigint NOT NULL CHECK(byte_count BETWEEN 1 AND 33554432),
 chunk_count integer NOT NULL CHECK(chunk_count BETWEEN 1 AND 32),
 row_count integer NOT NULL CHECK(row_count BETWEEN 0 AND 10000),
 sha256 text NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'), PRIMARY KEY(task_id,fencing_token)
);
CREATE FUNCTION adtr.user_asset_export_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='user_assets.v3.export' AND NEW.state IN ('cancelled','failed','partial_failed','dead_letter') THEN
  DELETE FROM adtr.user_asset_export_chunks WHERE task_id=NEW.task_id;
  DELETE FROM adtr.user_asset_export_manifests WHERE task_id=NEW.task_id;
  DELETE FROM adtr.user_asset_export_snapshots WHERE task_id=NEW.task_id;
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER user_asset_export_cleanup AFTER UPDATE OF state ON adtr.tasks
 FOR EACH ROW EXECUTE FUNCTION adtr.user_asset_export_cleanup();
`
