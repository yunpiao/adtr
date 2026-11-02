package simulationdetection

// SchemaSQL is migration 26. History and task payloads retain immutable evidence.
const SchemaSQL = `
CREATE TABLE adtr.simulation_detection_templates (
 tenant_id text NOT NULL,domain_id text NOT NULL,template_id text NOT NULL,
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 1024),archived boolean NOT NULL DEFAULT false,
 PRIMARY KEY(tenant_id,domain_id,template_id));
CREATE TABLE adtr.simulation_detection_versions (
 tenant_id text NOT NULL,domain_id text NOT NULL,template_id text NOT NULL,
 version bigint NOT NULL CHECK(version BETWEEN 1 AND 1024),hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'),
 body jsonb NOT NULL CHECK(jsonb_typeof(body)='object'),
 PRIMARY KEY(tenant_id,domain_id,template_id,version),
 FOREIGN KEY(tenant_id,domain_id,template_id) REFERENCES adtr.simulation_detection_templates(tenant_id,domain_id,template_id));
CREATE TABLE adtr.simulation_detection_receipts (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL CHECK(actor_id>0),operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 PRIMARY KEY(tenant_id,domain_id,actor_id,operation_id));
CREATE FUNCTION adtr.check_simulation_detection_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'detection templates cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.domain_id,NEW.template_id) IS DISTINCT FROM (OLD.tenant_id,OLD.domain_id,OLD.template_id)
 OR OLD.archived OR NOT ((NEW.version=OLD.version+1 AND NOT NEW.archived) OR (NEW.version=OLD.version AND NEW.archived))
 THEN RAISE EXCEPTION 'invalid detection template transition'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER detection_template_updates BEFORE UPDATE OR DELETE ON adtr.simulation_detection_templates FOR EACH ROW EXECUTE FUNCTION adtr.check_simulation_detection_update();
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_detection_versions','simulation_detection_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_detection_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['simulation_detection_templates','simulation_detection_versions','simulation_detection_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_detection_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
