package domains

// DirectoryObservationSchema is installed by migration15 together with the directory
// ledger/purpose/permission fragments under the exclusive migration gate.
const DirectoryObservationSchema = `
CREATE TABLE adtr.domain_directory_observations (
 tenant_id text NOT NULL, domain_id text NOT NULL, task_id text PRIMARY KEY,
 actor_id bigint NOT NULL CHECK(actor_id>0), opener_owner text NOT NULL CHECK(opener_owner<>''),
 opener_fencing_token bigint NOT NULL CHECK(opener_fencing_token>0), opener_attempt integer NOT NULL CHECK(opener_attempt=1),
 object_count integer NOT NULL CHECK(object_count BETWEEN 0 AND 10000),
 body bytea NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 16777216),
 sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,domain_id,task_id) REFERENCES adtr.domain_directory_task_uses(tenant_id,domain_id,task_id)
);
CREATE INDEX domain_directory_observations_latest ON adtr.domain_directory_observations(tenant_id,domain_id,created_at DESC,task_id);
CREATE FUNCTION adtr.domain_directory_observation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t adtr.tasks%ROWTYPE; u adtr.domain_directory_task_uses%ROWTYPE; b jsonb;
BEGIN
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 SELECT * INTO t FROM adtr.tasks WHERE task_id=NEW.task_id FOR UPDATE NOWAIT;
 SELECT * INTO u FROM adtr.domain_directory_task_uses WHERE task_id=NEW.task_id FOR UPDATE NOWAIT;
 IF t.task_id IS NULL OR u.task_id IS NULL OR t.state<>'running' OR t.lease_until IS NULL OR t.lease_until<=clock_timestamp()
 OR ROW(t.tenant_id,t.domain_id,t.actor_id,t.lease_owner,t.fencing_token,t.attempt) IS DISTINCT FROM ROW(NEW.tenant_id,NEW.domain_id,NEW.actor_id,NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt)
 OR u.state<>'opened' OR ROW(u.opener_owner,u.opener_fencing_token,u.opener_attempt) IS DISTINCT FROM ROW(NEW.opener_owner,NEW.opener_fencing_token,NEW.opener_attempt)
 OR NOT adtr.domain_directory_use_pins(u) OR NOT adtr.domain_directory_use_current(u) THEN RAISE EXCEPTION 'directory publication authority changed' USING ERRCODE='42501'; END IF;
 b:=convert_from(NEW.body,'UTF8')::jsonb;
 IF jsonb_typeof(b) IS DISTINCT FROM 'object' OR jsonb_typeof(b->'objects') IS DISTINCT FROM 'array' OR jsonb_typeof(b->'source') IS DISTINCT FROM 'object' THEN RAISE EXCEPTION 'invalid directory observation shape' USING ERRCODE='23514';END IF;
 IF (SELECT count(*) FROM jsonb_object_keys(b))<>2 OR jsonb_array_length(b->'objects')<>NEW.object_count
 OR NOT EXISTS(SELECT FROM adtr.domain_connections c WHERE c.tenant_id=NEW.tenant_id AND c.domain_id=NEW.domain_id AND b->'source'->>'domain'=c.canonical_domain AND b->'source'->>'server_name'=c.dc_hostname) THEN RAISE EXCEPTION 'directory observation source mismatch' USING ERRCODE='23514';END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_directory_observation_guard BEFORE INSERT ON adtr.domain_directory_observations FOR EACH ROW EXECUTE FUNCTION adtr.domain_directory_observation_guard();
CREATE TRIGGER domain_directory_observations_immutable BEFORE UPDATE OR DELETE ON adtr.domain_directory_observations FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_directory_observations_no_truncate BEFORE TRUNCATE ON adtr.domain_directory_observations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
