package simulationlabels

// SchemaSQL is installed only by the explicit application migrator. It does not
// assign permissions, load fixtures, change existing tables or execute at startup.
const SchemaSQL = `
CREATE TABLE adtr.simulation_label_datasets (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL,
 fixture_hash text NOT NULL CHECK(fixture_hash ~ '^[0-9a-f]{64}$'), payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id), UNIQUE(tenant_id,domain_id,dataset_id,fixture_hash)
);
CREATE TABLE adtr.simulation_label_observations (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 observation_id text NOT NULL, sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 128),
 observation_hash text NOT NULL CHECK(observation_hash ~ '^[0-9a-f]{64}$'), payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,observation_id), UNIQUE(tenant_id,domain_id,dataset_id,sequence),
 UNIQUE(tenant_id,domain_id,dataset_id,observation_id,observation_hash),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash) REFERENCES adtr.simulation_label_datasets(tenant_id,domain_id,dataset_id,fixture_hash)
);
CREATE TABLE adtr.simulation_label_policies (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, policy_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 1024),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,policy_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id) REFERENCES adtr.simulation_label_datasets(tenant_id,domain_id,dataset_id)
);
CREATE TABLE adtr.simulation_label_policy_versions (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, policy_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 1024), policy_hash text NOT NULL CHECK(policy_hash ~ '^[0-9a-f]{64}$'),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,policy_id,revision),
 UNIQUE(tenant_id,domain_id,dataset_id,policy_id,revision,policy_hash),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,policy_id) REFERENCES adtr.simulation_label_policies(tenant_id,domain_id,dataset_id,policy_id)
);
ALTER TABLE adtr.simulation_label_policies ADD CONSTRAINT simulation_label_current_revision
 FOREIGN KEY(tenant_id,domain_id,dataset_id,policy_id,revision) REFERENCES adtr.simulation_label_policy_versions(tenant_id,domain_id,dataset_id,policy_id,revision) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE adtr.simulation_label_evaluations (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, evaluation_id text NOT NULL,
 policy_id text NOT NULL, revision bigint NOT NULL, policy_hash text NOT NULL,
 observation_id text NOT NULL, observation_hash text NOT NULL,
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,evaluation_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,policy_id,revision,policy_hash) REFERENCES adtr.simulation_label_policy_versions(tenant_id,domain_id,dataset_id,policy_id,revision,policy_hash),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,observation_id,observation_hash) REFERENCES adtr.simulation_label_observations(tenant_id,domain_id,dataset_id,observation_id,observation_hash)
);
CREATE TABLE adtr.simulation_label_comparisons (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, comparison_id text NOT NULL,
 before_id text NOT NULL, after_id text NOT NULL, payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,comparison_id), UNIQUE(tenant_id,domain_id,dataset_id,before_id,after_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,before_id) REFERENCES adtr.simulation_label_evaluations(tenant_id,domain_id,dataset_id,evaluation_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,after_id) REFERENCES adtr.simulation_label_evaluations(tenant_id,domain_id,dataset_id,evaluation_id)
);
CREATE TABLE adtr.simulation_label_receipts (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, actor text NOT NULL, operation_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'), request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'), payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,actor,operation_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id) REFERENCES adtr.simulation_label_datasets(tenant_id,domain_id,dataset_id)
);
`
const ImmutableSchemaSQL = `
CREATE FUNCTION adtr.reject_simulation_label_mutation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable simulation label record'; END $$;
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_label_datasets','simulation_label_observations','simulation_label_policy_versions','simulation_label_evaluations','simulation_label_comparisons','simulation_label_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_simulation_label_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_label_mutation()',t);
 EXECUTE format('CREATE TRIGGER immutable_simulation_label_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_label_mutation()',t);
 END LOOP;
END $$;
`
