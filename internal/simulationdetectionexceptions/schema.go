package simulationdetectionexceptions

const SchemaSQL = `
CREATE TABLE adtr.simulation_exception_heads (
 tenant_id text NOT NULL,domain_id text NOT NULL,revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 256),
 PRIMARY KEY(tenant_id,domain_id));
CREATE TABLE adtr.simulation_exception_revisions (
 tenant_id text NOT NULL,domain_id text NOT NULL,revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 256),
 hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,revision),FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.simulation_exception_heads);
CREATE TABLE adtr.simulation_exception_evaluations (
 tenant_id text NOT NULL,domain_id text NOT NULL,evaluation_id text NOT NULL,actor_id bigint NOT NULL CHECK(actor_id>0),
 revision bigint NOT NULL,hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,evaluation_id),FOREIGN KEY(tenant_id,domain_id,revision) REFERENCES adtr.simulation_exception_revisions);
CREATE TABLE adtr.simulation_exception_receipts (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL CHECK(actor_id>0),operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=524288),
 PRIMARY KEY(tenant_id,domain_id,actor_id,operation_id));
CREATE FUNCTION adtr.check_simulation_exception_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'exception history cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.domain_id) IS DISTINCT FROM (OLD.tenant_id,OLD.domain_id) OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'invalid exception revision'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER simulation_exception_head_update BEFORE UPDATE OR DELETE ON adtr.simulation_exception_heads FOR EACH ROW EXECUTE FUNCTION adtr.check_simulation_exception_head();
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_exception_revisions','simulation_exception_evaluations','simulation_exception_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_exception_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['simulation_exception_heads','simulation_exception_revisions','simulation_exception_evaluations','simulation_exception_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_exception_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
