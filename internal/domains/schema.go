package domains

// Schema runs only in migration 9, under the shared migration gate. There is no
// startup, readiness or request-time DDL. History contains metadata only.
const Schema = `
CREATE TABLE adtr.domain_connections (
 tenant_id text NOT NULL, domain_id text NOT NULL,
 canonical_domain text NOT NULL, dc_hostname text NOT NULL, dial_ip text NOT NULL DEFAULT '',
 transport_mode text NOT NULL CHECK(transport_mode IN ('starttls','ldaps')),
 port text NOT NULL CHECK(port IN ('389','636')),
 connection_revision bigint NOT NULL DEFAULT 1 CHECK(connection_revision>0),
 credential_revision bigint NOT NULL DEFAULT 1 CHECK(credential_revision>0),
 credential_mode text NOT NULL DEFAULT 'custom' CHECK(credential_mode='custom'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 deleted_at timestamptz, latest_test_task_id text, diagnostic_generation bigint NOT NULL DEFAULT 1 CHECK(diagnostic_generation>0),
 PRIMARY KEY(tenant_id,domain_id),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.resource_domains(tenant_id,id),
 CHECK((port='389' AND transport_mode='starttls') OR (port='636' AND transport_mode='ldaps')),
 CHECK(length(canonical_domain) BETWEEN 3 AND 253), CHECK(length(dc_hostname) BETWEEN 3 AND 253)
);
CREATE UNIQUE INDEX domain_connections_live_name ON adtr.domain_connections(tenant_id,canonical_domain) WHERE deleted_at IS NULL;
CREATE TABLE adtr.domain_credentials (
 tenant_id text NOT NULL, domain_id text NOT NULL, credential_revision bigint NOT NULL CHECK(credential_revision>0),
 key_id text NOT NULL CHECK(length(key_id) BETWEEN 1 AND 64), ciphertext bytea NOT NULL CHECK(octet_length(ciphertext)>28),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,domain_id),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.domain_connections(tenant_id,domain_id)
);
CREATE TABLE adtr.domain_creation_receipts (
 tenant_id text NOT NULL, actor_id bigint NOT NULL, idempotency_key text NOT NULL,
 fingerprint text NOT NULL CHECK(length(fingerprint)=64), domain_id text NOT NULL,
 connection_revision bigint NOT NULL CHECK(connection_revision>0), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.domain_connections(tenant_id,domain_id)
);
CREATE TABLE adtr.domain_diagnostics (
 tenant_id text NOT NULL, domain_id text NOT NULL, task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 connection_revision bigint NOT NULL, credential_revision bigint NOT NULL, policy_revision text NOT NULL,
 diagnostic_generation bigint NOT NULL, stage text NOT NULL, code text NOT NULL,
 successful_observation boolean NOT NULL, dc_hostname text NOT NULL DEFAULT '', naming_context text NOT NULL DEFAULT '',
 observed_at timestamptz NOT NULL DEFAULT clock_timestamp(), elapsed_ms bigint NOT NULL CHECK(elapsed_ms>=0),
 PRIMARY KEY(tenant_id,domain_id,task_id), UNIQUE(task_id),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.domain_connections(tenant_id,domain_id),
 CHECK(stage IN ('authorization','resolve','connect','tls','bind','rootdse','identity','publish')),
 CHECK(code ~ '^[a-z][a-z0-9_]{0,63}$'), CHECK(length(dc_hostname)<=253), CHECK(length(naming_context)<=1024),
 CHECK(NOT successful_observation OR code='success'), CHECK(successful_observation OR (dc_hostname='' AND naming_context=''))
);
-- Future modules must register live dependencies explicitly before using this
-- configuration. Unknown live tasks also block local deletion.
CREATE TABLE adtr.domain_dependencies (
 tenant_id text NOT NULL, domain_id text NOT NULL, module text NOT NULL, object_id text NOT NULL,
 PRIMARY KEY(tenant_id,domain_id,module,object_id),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.domain_connections(tenant_id,domain_id)
);
CREATE TABLE adtr.domain_audit (
 id bigserial PRIMARY KEY, tenant_id text NOT NULL, actor_id bigint NOT NULL,
 domain_id text NOT NULL, action text NOT NULL CHECK(action IN ('domain_create','domain_update','domain_delete','domain_test_submit','domain_test_result')),
 old_revision bigint NOT NULL, new_revision bigint NOT NULL, credential_revision bigint NOT NULL,
 task_id text NOT NULL DEFAULT '', result text NOT NULL CHECK(result ~ '^[a-z][a-z0-9_]{0,63}$'),
 audit_username text, audit_ip text, audit_path text, audit_request_id text,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION adtr.domain_connection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'domain identity is durable' USING ERRCODE='42501'; END IF;
 IF ROW(NEW.tenant_id,NEW.domain_id,NEW.canonical_domain,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.domain_id,OLD.canonical_domain,OLD.created_at)
 OR NEW.connection_revision<OLD.connection_revision OR NEW.credential_revision<OLD.credential_revision OR NEW.diagnostic_generation<OLD.diagnostic_generation
 OR OLD.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'domain identity or revision is immutable' USING ERRCODE='42501'; END IF;
 IF ROW(NEW.dc_hostname,NEW.dial_ip,NEW.transport_mode,NEW.port,NEW.credential_revision,NEW.deleted_at)
 IS DISTINCT FROM ROW(OLD.dc_hostname,OLD.dial_ip,OLD.transport_mode,OLD.port,OLD.credential_revision,OLD.deleted_at)
 AND NEW.connection_revision<>OLD.connection_revision+1 THEN RAISE EXCEPTION 'domain revision must advance' USING ERRCODE='42501'; END IF;
 IF NEW.credential_revision<>OLD.credential_revision AND NEW.credential_revision<>OLD.credential_revision+1 THEN RAISE EXCEPTION 'credential revision must advance' USING ERRCODE='42501'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_connection_guard BEFORE UPDATE OR DELETE ON adtr.domain_connections FOR EACH ROW EXECUTE FUNCTION adtr.domain_connection_guard();
CREATE FUNCTION adtr.domain_connection_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.connection_revision,NEW.credential_revision,NEW.deleted_at) IS DISTINCT FROM ROW(OLD.connection_revision,OLD.credential_revision,OLD.deleted_at) THEN
  PERFORM adtr.task_auth_bump(NEW.tenant_id,NULL);
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER domain_connection_epoch AFTER UPDATE ON adtr.domain_connections FOR EACH ROW EXECUTE FUNCTION adtr.domain_connection_epoch();
CREATE TRIGGER domain_audit_immutable BEFORE UPDATE OR DELETE ON adtr.domain_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_audit_capture BEFORE INSERT ON adtr.domain_audit FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
CREATE TRIGGER domain_audit_no_truncate BEFORE TRUNCATE ON adtr.domain_audit FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_receipts_immutable BEFORE UPDATE OR DELETE ON adtr.domain_creation_receipts FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_receipts_no_truncate BEFORE TRUNCATE ON adtr.domain_creation_receipts FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_diagnostics_immutable BEFORE UPDATE OR DELETE ON adtr.domain_diagnostics FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_diagnostics_no_truncate BEFORE TRUNCATE ON adtr.domain_diagnostics FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER domain_connections_no_truncate BEFORE TRUNCATE ON adtr.domain_connections FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
