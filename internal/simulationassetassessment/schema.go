package simulationassetassessment

const SchemaSQL = `
CREATE TABLE adtr.simulation_asset_observations (
 tenant_id text NOT NULL, domain_id text NOT NULL, observation_id text NOT NULL,
 hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'), body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,observation_id));
CREATE TABLE adtr.simulation_asset_reports (
 tenant_id text NOT NULL, domain_id text NOT NULL, actor_id bigint NOT NULL CHECK(actor_id>0), report_id text NOT NULL,
 epoch bigint NOT NULL CHECK(epoch>0), hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,actor_id,report_id));
CREATE TABLE adtr.simulation_asset_receipts (
 tenant_id text NOT NULL, domain_id text NOT NULL, actor_id bigint NOT NULL CHECK(actor_id>0), operation_id text NOT NULL,
 epoch bigint NOT NULL CHECK(epoch>0), request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'), body jsonb NOT NULL CHECK(jsonb_typeof(body)='object' AND octet_length(body::text)<=262144),
 PRIMARY KEY(tenant_id,domain_id,actor_id,operation_id));
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_asset_observations','simulation_asset_reports','simulation_asset_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_asset_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 EXECUTE format('CREATE TRIGGER immutable_asset_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
