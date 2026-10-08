package operationaccounts

// Schema is applied only by migration 10. Stage A reserves a metadata-only
// dependency table but exposes no consumer-admission or secret-use API.
const Schema = `
CREATE TABLE adtr.operation_accounts (
 tenant_id text NOT NULL, domain_id text NOT NULL, account_id text NOT NULL,
 label text NOT NULL DEFAULT '' CHECK(char_length(label)<=50 AND octet_length(label)<=200),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 credential_revision bigint NOT NULL DEFAULT 1 CHECK(credential_revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(), deleted_at timestamptz,
 PRIMARY KEY(tenant_id,domain_id,account_id), UNIQUE(tenant_id,account_id),
 UNIQUE(tenant_id,domain_id,account_id,credential_revision),
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.domain_connections(tenant_id,domain_id)
);
CREATE INDEX operation_accounts_live_page ON adtr.operation_accounts(tenant_id,created_at DESC,account_id DESC) WHERE deleted_at IS NULL;
CREATE TABLE adtr.operation_account_credentials (
 tenant_id text NOT NULL, domain_id text NOT NULL, account_id text NOT NULL,
 credential_revision bigint NOT NULL CHECK(credential_revision>0), envelope_version integer NOT NULL DEFAULT 2 CHECK(envelope_version=2),
 key_id text NOT NULL CHECK(length(key_id) BETWEEN 1 AND 64), ciphertext bytea NOT NULL CHECK(octet_length(ciphertext)>28),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,domain_id,account_id),
 FOREIGN KEY(tenant_id,domain_id,account_id,credential_revision) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id,credential_revision) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE adtr.operation_account_mutations (
 tenant_id text NOT NULL, actor_id bigint NOT NULL, idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 128),
 operation text NOT NULL CHECK(operation IN ('create','update','delete')), fingerprint text NOT NULL CHECK(fingerprint ~ '^[0-9a-f]{64}$'),
 domain_id text NOT NULL, account_id text NOT NULL, revision bigint NOT NULL CHECK(revision>0), credential_revision bigint NOT NULL CHECK(credential_revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id)
);
CREATE TABLE adtr.operation_account_dependencies (
 tenant_id text NOT NULL, domain_id text NOT NULL, account_id text NOT NULL,
 consumer_kind text NOT NULL CHECK(consumer_kind ~ '^[a-z][a-z0-9_.]{0,63}$'), object_id text NOT NULL CHECK(length(object_id) BETWEEN 1 AND 128),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,domain_id,account_id,consumer_kind,object_id),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id)
);
CREATE TABLE adtr.operation_account_audit (
 id bigserial PRIMARY KEY, tenant_id text NOT NULL, actor_id bigint NOT NULL, domain_id text NOT NULL, account_id text NOT NULL,
 action text NOT NULL CHECK(action IN ('operation_account_create','operation_account_update','operation_account_delete')),
 old_revision bigint NOT NULL CHECK(old_revision>=0), new_revision bigint NOT NULL CHECK(new_revision>0),
 old_credential_revision bigint NOT NULL CHECK(old_credential_revision>=0), new_credential_revision bigint NOT NULL CHECK(new_credential_revision>0),
 result text NOT NULL CHECK(result IN ('saved','deleted')),
 audit_username text, audit_ip text, audit_path text, audit_request_id text,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(tenant_id,domain_id,account_id) REFERENCES adtr.operation_accounts(tenant_id,domain_id,account_id)
);
CREATE FUNCTION adtr.operation_account_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE parent_live boolean;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'account identity is durable' USING ERRCODE='42501'; END IF;
 PERFORM adtr.task_auth_lock(NEW.tenant_id);
 SELECT c.deleted_at IS NULL AND d.active INTO parent_live FROM adtr.domain_connections c
 JOIN adtr.resource_domains d ON d.tenant_id=c.tenant_id AND d.id=c.domain_id
 WHERE c.tenant_id=NEW.tenant_id AND c.domain_id=NEW.domain_id FOR UPDATE OF c NOWAIT;
 IF NOT COALESCE(parent_live,false) THEN RAISE EXCEPTION 'account parent unavailable' USING ERRCODE='23514'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.revision<>1 OR NEW.credential_revision<>1 OR NEW.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'invalid initial account revision' USING ERRCODE='23514'; END IF;
  RETURN NEW;
 END IF;
 IF ROW(NEW.tenant_id,NEW.domain_id,NEW.account_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.domain_id,OLD.account_id,OLD.created_at)
 OR OLD.deleted_at IS NOT NULL THEN RAISE EXCEPTION 'account identity is immutable' USING ERRCODE='42501'; END IF;
 IF ROW(NEW.label,NEW.credential_revision,NEW.deleted_at) IS DISTINCT FROM ROW(OLD.label,OLD.credential_revision,OLD.deleted_at) THEN
  IF NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'account revision must advance' USING ERRCODE='23514'; END IF;
 ELSIF NEW.revision<>OLD.revision THEN RAISE EXCEPTION 'account revision has no change' USING ERRCODE='23514'; END IF;
 IF NEW.credential_revision<>OLD.credential_revision AND NEW.credential_revision<>OLD.credential_revision+1 THEN RAISE EXCEPTION 'credential revision must advance' USING ERRCODE='23514'; END IF;
 IF NEW.deleted_at IS NOT NULL AND NEW.credential_revision<>OLD.credential_revision+1 THEN RAISE EXCEPTION 'deletion must advance credential revision' USING ERRCODE='23514'; END IF;
 IF ROW(NEW.credential_revision,NEW.deleted_at) IS DISTINCT FROM ROW(OLD.credential_revision,OLD.deleted_at)
 AND EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE tenant_id=OLD.tenant_id AND domain_id=OLD.domain_id AND account_id=OLD.account_id)
 THEN RAISE EXCEPTION 'account has consumers' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER operation_account_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.operation_accounts FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_guard();
CREATE FUNCTION adtr.operation_account_epoch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' OR ROW(NEW.credential_revision,NEW.deleted_at) IS DISTINCT FROM ROW(OLD.credential_revision,OLD.deleted_at) THEN PERFORM adtr.task_auth_bump(NEW.tenant_id,NULL); END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER operation_account_epoch AFTER INSERT OR UPDATE ON adtr.operation_accounts FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_epoch();
CREATE FUNCTION adtr.operation_account_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text; a text; dead timestamptz; rev bigint;
BEGIN
 IF TG_OP='DELETE' THEN t:=OLD.tenant_id; d:=OLD.domain_id; a:=OLD.account_id; ELSE t:=NEW.tenant_id; d:=NEW.domain_id; a:=NEW.account_id; END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.tenant_id,NEW.domain_id,NEW.account_id) IS DISTINCT FROM ROW(OLD.tenant_id,OLD.domain_id,OLD.account_id) THEN RAISE EXCEPTION 'account child identity is immutable' USING ERRCODE='42501'; END IF;
 PERFORM adtr.task_auth_lock(t);
 PERFORM 1 FROM adtr.domain_connections WHERE tenant_id=t AND domain_id=d FOR UPDATE NOWAIT;
 SELECT deleted_at,credential_revision INTO dead,rev FROM adtr.operation_accounts WHERE tenant_id=t AND domain_id=d AND account_id=a FOR UPDATE NOWAIT;
 IF TG_OP<>'DELETE' AND (NOT FOUND OR dead IS NOT NULL) THEN RAISE EXCEPTION 'account unavailable' USING ERRCODE='23514'; END IF;
 IF TG_TABLE_NAME='operation_account_credentials' THEN
  IF TG_OP='INSERT' AND (NEW.credential_revision<>1 OR rev<>1) THEN RAISE EXCEPTION 'credential insertion requires initial revision' USING ERRCODE='23514'; END IF;
  IF TG_OP='UPDATE' AND NEW.credential_revision<>OLD.credential_revision+1 THEN RAISE EXCEPTION 'credential replacement must advance revision' USING ERRCODE='23514'; END IF;
  IF TG_OP='DELETE' AND dead IS NULL THEN RAISE EXCEPTION 'live credential removal requires account deletion' USING ERRCODE='23514'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER operation_account_credentials_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.operation_account_credentials FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_child_guard();
CREATE TRIGGER operation_account_dependencies_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.operation_account_dependencies FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_child_guard();
CREATE FUNCTION adtr.operation_account_pin_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text;
BEGIN
 IF TG_OP='UPDATE' AND (OLD.module='operation_accounts' OR NEW.module='operation_accounts') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'account pin identity is immutable' USING ERRCODE='42501'; END IF;
 IF TG_OP='DELETE' THEN IF OLD.module<>'operation_accounts' THEN RETURN OLD; END IF; t:=OLD.tenant_id; d:=OLD.domain_id;
 ELSE IF NEW.module<>'operation_accounts' THEN RETURN NEW; END IF; t:=NEW.tenant_id; d:=NEW.domain_id; END IF;
 PERFORM adtr.task_auth_lock(t);
 PERFORM 1 FROM adtr.domain_connections WHERE tenant_id=t AND domain_id=d FOR UPDATE NOWAIT;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER operation_account_pin_guard BEFORE INSERT OR UPDATE OR DELETE ON adtr.domain_dependencies FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_pin_guard();
-- Deferred checks allow a single transaction to replace/delete the pair and its
-- parent revision, but make an orphan, missing pair, or missing pin impossible.
CREATE FUNCTION adtr.operation_account_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text; a text; rev bigint; dead timestamptz; present boolean; pinned boolean;
BEGIN
 IF TG_TABLE_NAME='domain_dependencies' THEN
  IF TG_OP='DELETE' THEN IF OLD.module<>'operation_accounts' THEN RETURN NULL; END IF; t:=OLD.tenant_id; d:=OLD.domain_id; a:=OLD.object_id;
  ELSE IF NEW.module<>'operation_accounts' THEN RETURN NULL; END IF; t:=NEW.tenant_id; d:=NEW.domain_id; a:=NEW.object_id; END IF;
 ELSE
  IF TG_OP='DELETE' THEN t:=OLD.tenant_id; d:=OLD.domain_id; a:=OLD.account_id; ELSE t:=NEW.tenant_id; d:=NEW.domain_id; a:=NEW.account_id; END IF;
 END IF;
 SELECT credential_revision,deleted_at INTO rev,dead FROM adtr.operation_accounts WHERE tenant_id=t AND domain_id=d AND account_id=a;
 IF NOT FOUND THEN RAISE EXCEPTION 'account dependency has no identity' USING ERRCODE='23514'; END IF;
 SELECT EXISTS(SELECT FROM adtr.operation_account_credentials WHERE tenant_id=t AND domain_id=d AND account_id=a AND credential_revision=rev),
 EXISTS(SELECT FROM adtr.domain_dependencies WHERE tenant_id=t AND domain_id=d AND module='operation_accounts' AND object_id=a) INTO present,pinned;
 IF (dead IS NULL AND (NOT present OR NOT pinned)) OR (dead IS NOT NULL AND (present OR pinned OR EXISTS(SELECT FROM adtr.operation_account_credentials WHERE tenant_id=t AND domain_id=d AND account_id=a))) THEN RAISE EXCEPTION 'account credential or domain pin inconsistent' USING ERRCODE='23514'; END IF;
 RETURN NULL;
END; $$;
CREATE CONSTRAINT TRIGGER operation_account_consistency AFTER INSERT OR UPDATE ON adtr.operation_accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_consistency();
CREATE CONSTRAINT TRIGGER operation_account_pair_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.operation_account_credentials DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_consistency();
CREATE CONSTRAINT TRIGGER operation_account_pin_consistency AFTER INSERT OR UPDATE OR DELETE ON adtr.domain_dependencies DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_consistency();
CREATE FUNCTION adtr.operation_account_parent_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE t text; d text; removing boolean;
BEGIN
 t:=OLD.tenant_id;
 IF TG_TABLE_NAME='domain_connections' THEN d:=OLD.domain_id; removing:=TG_OP='DELETE'; IF TG_OP='UPDATE' THEN removing:=NEW.deleted_at IS NOT NULL; END IF;
 ELSE d:=OLD.id; removing:=TG_OP='DELETE'; IF TG_OP='UPDATE' THEN removing:=NOT NEW.active; END IF; END IF;
 IF removing THEN
  PERFORM adtr.task_auth_lock(t);
  IF EXISTS(SELECT FROM adtr.operation_accounts WHERE tenant_id=t AND domain_id=d AND deleted_at IS NULL) THEN RAISE EXCEPTION 'domain has operation accounts' USING ERRCODE='23514'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END; $$;
CREATE TRIGGER operation_accounts_parent_guard BEFORE UPDATE OR DELETE ON adtr.domain_connections FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_parent_guard();
CREATE TRIGGER operation_accounts_catalogue_guard BEFORE UPDATE OR DELETE ON adtr.resource_domains FOR EACH ROW EXECUTE FUNCTION adtr.operation_account_parent_guard();
CREATE TRIGGER operation_accounts_no_truncate BEFORE TRUNCATE ON adtr.operation_accounts FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_credentials_no_truncate BEFORE TRUNCATE ON adtr.operation_account_credentials FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_dependencies_no_truncate BEFORE TRUNCATE ON adtr.operation_account_dependencies FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_domain_pins_no_truncate BEFORE TRUNCATE ON adtr.domain_dependencies FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_receipts_immutable BEFORE UPDATE OR DELETE ON adtr.operation_account_mutations FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_receipts_no_truncate BEFORE TRUNCATE ON adtr.operation_account_mutations FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_audit_immutable BEFORE UPDATE OR DELETE ON adtr.operation_account_audit FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_audit_no_truncate BEFORE TRUNCATE ON adtr.operation_account_audit FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER operation_account_audit_capture BEFORE INSERT ON adtr.operation_account_audit FOR EACH ROW EXECUTE FUNCTION adtr.capture_audit_metadata();
`
