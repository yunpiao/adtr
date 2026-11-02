package simulationdnsip

const SchemaSQL = `
CREATE TABLE adtr.simulation_dns_ip_observations (
 tenant_id text NOT NULL,domain_id text NOT NULL,observation_id text NOT NULL,
 hash text NOT NULL CHECK(hash ~ '^[0-9a-f]{64}$'),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object'),
 PRIMARY KEY(tenant_id,domain_id,observation_id),CHECK(octet_length(body::text)<=8388608));
CREATE TABLE adtr.simulation_dns_ip_receipts (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL CHECK(actor_id>0),operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 action text NOT NULL CHECK(action IN ('export','cancel')),body jsonb NOT NULL CHECK(jsonb_typeof(body)='object'),
 PRIMARY KEY(tenant_id,domain_id,actor_id,operation_id),CHECK(octet_length(body::text)<=1048576));
CREATE UNIQUE INDEX simulation_dns_ip_one_export_intent ON adtr.simulation_dns_ip_receipts(task_id) WHERE action='export';
CREATE TABLE adtr.simulation_dns_ip_artifacts (
 tenant_id text NOT NULL,domain_id text NOT NULL,actor_id bigint NOT NULL CHECK(actor_id>0),task_id text NOT NULL REFERENCES adtr.tasks(task_id),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object'),workbook bytea NOT NULL CHECK(octet_length(workbook) BETWEEN 1 AND 8388608),
 PRIMARY KEY(task_id),CHECK(octet_length(manifest::text)<=16384));
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_dns_ip_observations','simulation_dns_ip_receipts','simulation_dns_ip_artifacts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_dns_ip_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 EXECUTE format('CREATE TRIGGER immutable_dns_ip_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
