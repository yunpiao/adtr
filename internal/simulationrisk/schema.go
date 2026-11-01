package simulationrisk

// SchemaSQL requires migration 24's immutable dataset tables. No grants change.
const SchemaSQL = `
CREATE TABLE adtr.simulation_risk_resets (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 subject text NOT NULL CHECK(octet_length(subject) BETWEEN 1 AND 128),
 revision bigint NOT NULL CHECK(revision BETWEEN 2 AND 1025),
 reset_id text NOT NULL, actor text NOT NULL, operation_id text NOT NULL,
 reason text NOT NULL CHECK(octet_length(reason) BETWEEN 1 AND 512),
 at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,domain_id,dataset_id,fixture_hash,subject,revision),
 UNIQUE(tenant_id,domain_id,dataset_id,actor,operation_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash) REFERENCES adtr.simulation_event_datasets(tenant_id,domain_id,dataset_id,fixture_hash)
);
CREATE TABLE adtr.simulation_risk_receipts (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 subject text NOT NULL, revision bigint NOT NULL,
 actor text NOT NULL, operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,actor,operation_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash,subject,revision) REFERENCES adtr.simulation_risk_resets(tenant_id,domain_id,dataset_id,fixture_hash,subject,revision),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,actor,operation_id) REFERENCES adtr.simulation_risk_resets(tenant_id,domain_id,dataset_id,actor,operation_id)
);
`
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_risk_resets','simulation_risk_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_simulation_risk_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 EXECUTE format('CREATE TRIGGER immutable_simulation_risk_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
