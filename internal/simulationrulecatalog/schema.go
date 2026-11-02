package simulationrulecatalog

// SchemaSQL is installed only by the explicit migration owner, never an API.
const SchemaSQL = `
CREATE TABLE adtr.simulation_rule_catalog_heads (
 tenant_id text NOT NULL,domain_id text NOT NULL,rule_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 64),summary jsonb NOT NULL CHECK(jsonb_typeof(summary)='object'),
 PRIMARY KEY(tenant_id,domain_id,rule_id));
CREATE TABLE adtr.simulation_rule_catalog_revisions (
 tenant_id text NOT NULL,domain_id text NOT NULL,rule_id text NOT NULL,revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 64),
 hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,rule_id,revision),
 FOREIGN KEY(tenant_id,domain_id,rule_id) REFERENCES adtr.simulation_rule_catalog_heads);
CREATE TABLE adtr.simulation_rule_catalog_publications (
 tenant_id text NOT NULL,domain_id text NOT NULL,rule_id text NOT NULL,version bigint NOT NULL CHECK(version>0),
 digest text NOT NULL CHECK(digest ~ '^[0-9a-f]{64}$'),test_revision bigint NOT NULL,body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,rule_id,version),
 FOREIGN KEY(tenant_id,domain_id,rule_id,test_revision) REFERENCES adtr.simulation_rule_catalog_revisions);
CREATE TABLE adtr.simulation_rule_catalog_receipts (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL CHECK(actor_id>0),operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=524288),
 PRIMARY KEY(tenant_id,domain_id,actor_id,operation_id));
CREATE TABLE adtr.simulation_rule_catalog_origins (
 tenant_id text NOT NULL,domain_id text NOT NULL,template_id text NOT NULL,template_version bigint NOT NULL,
 rule_id text NOT NULL,published_version bigint NOT NULL,body jsonb NOT NULL CHECK(jsonb_typeof(body)='object'),
 PRIMARY KEY(tenant_id,domain_id,template_id,template_version),
 FOREIGN KEY(tenant_id,domain_id,template_id,template_version) REFERENCES adtr.simulation_detection_versions,
 FOREIGN KEY(tenant_id,domain_id,rule_id,published_version) REFERENCES adtr.simulation_rule_catalog_publications);
CREATE FUNCTION adtr.check_simulation_rule_catalog_head() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'catalog history cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.domain_id,NEW.rule_id) IS DISTINCT FROM (OLD.tenant_id,OLD.domain_id,OLD.rule_id) OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'invalid catalog revision'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER simulation_rule_catalog_head_update BEFORE UPDATE OR DELETE ON adtr.simulation_rule_catalog_heads FOR EACH ROW EXECUTE FUNCTION adtr.check_simulation_rule_catalog_head();
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_rule_catalog_revisions','simulation_rule_catalog_publications','simulation_rule_catalog_receipts','simulation_rule_catalog_origins'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_catalog_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
 FOREACH t IN ARRAY ARRAY['simulation_rule_catalog_heads','simulation_rule_catalog_revisions','simulation_rule_catalog_publications','simulation_rule_catalog_receipts','simulation_rule_catalog_origins'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_catalog_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
