package simulationevents

// SchemaSQL is applied only by explicit migration 24. All fixture-bound foreign
// keys include tenant, domain, dataset and fixture hash. No existing grants change.
const SchemaSQL = `
CREATE TABLE adtr.simulation_event_datasets (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL,
 fixture text NOT NULL CHECK(octet_length(fixture) BETWEEN 1 AND 128),
 fixture_hash text NOT NULL CHECK(fixture_hash ~ '^[0-9a-f]{64}$'),
 contract text NOT NULL CHECK(contract='adtr.securityevents.simulated.v1'),
 simulation boolean NOT NULL CHECK(simulation),
 event_count integer NOT NULL CHECK(event_count BETWEEN 0 AND 4096),
 PRIMARY KEY(tenant_id,domain_id,dataset_id),
 UNIQUE(tenant_id,domain_id,dataset_id,fixture_hash)
);
CREATE TABLE adtr.simulation_events (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 event_id text NOT NULL, payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,fixture_hash,event_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash) REFERENCES adtr.simulation_event_datasets(tenant_id,domain_id,dataset_id,fixture_hash)
);
CREATE TABLE adtr.simulation_event_cases (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 case_id text NOT NULL, state text NOT NULL CHECK(state IN ('open','closed')),
 mark text NOT NULL CHECK(mark IN ('','benign','suspicious')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 1025), updated_at timestamptz,
 PRIMARY KEY(tenant_id,domain_id,dataset_id,fixture_hash,case_id),
 CHECK((revision=1 AND state='open' AND mark='' AND updated_at IS NULL) OR (revision>1 AND updated_at IS NOT NULL)),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash) REFERENCES adtr.simulation_event_datasets(tenant_id,domain_id,dataset_id,fixture_hash)
);
CREATE TABLE adtr.simulation_event_case_evidence (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 case_id text NOT NULL, event_id text NOT NULL,
 PRIMARY KEY(tenant_id,domain_id,dataset_id,fixture_hash,case_id,event_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash,case_id) REFERENCES adtr.simulation_event_cases(tenant_id,domain_id,dataset_id,fixture_hash,case_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash,event_id) REFERENCES adtr.simulation_events(tenant_id,domain_id,dataset_id,fixture_hash,event_id)
);
CREATE TABLE adtr.simulation_event_transitions (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 case_id text NOT NULL, revision bigint NOT NULL CHECK(revision BETWEEN 2 AND 1025),
 audit jsonb NOT NULL CHECK(jsonb_typeof(audit)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,fixture_hash,case_id,revision),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash,case_id) REFERENCES adtr.simulation_event_cases(tenant_id,domain_id,dataset_id,fixture_hash,case_id)
);
CREATE TABLE adtr.simulation_event_receipts (
 tenant_id text NOT NULL, domain_id text NOT NULL, dataset_id text NOT NULL, fixture_hash text NOT NULL,
 actor text NOT NULL, operation_id text NOT NULL, case_id text NOT NULL,
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 PRIMARY KEY(tenant_id,domain_id,dataset_id,actor,operation_id),
 FOREIGN KEY(tenant_id,domain_id,dataset_id,fixture_hash,case_id) REFERENCES adtr.simulation_event_cases(tenant_id,domain_id,dataset_id,fixture_hash,case_id)
);
CREATE FUNCTION adtr.reject_simulation_event_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'simulated fixture/history is immutable'; END $$;
CREATE FUNCTION adtr.check_simulation_case_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'simulated cases cannot be deleted'; END IF;
 IF (NEW.tenant_id,NEW.domain_id,NEW.dataset_id,NEW.fixture_hash,NEW.case_id) IS DISTINCT FROM (OLD.tenant_id,OLD.domain_id,OLD.dataset_id,OLD.fixture_hash,OLD.case_id)
 OR OLD.state<>'open' OR NEW.revision<>OLD.revision+1 OR NEW.updated_at IS NULL OR NEW.updated_at<OLD.updated_at
 OR (NEW.state=OLD.state AND NEW.mark=OLD.mark) OR (NEW.state='closed' AND NEW.mark<>OLD.mark) THEN
 RAISE EXCEPTION 'invalid simulated case transition'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER simulation_case_updates BEFORE UPDATE OR DELETE ON adtr.simulation_event_cases FOR EACH ROW EXECUTE FUNCTION adtr.check_simulation_case_update();
CREATE TRIGGER simulation_cases_no_truncate BEFORE TRUNCATE ON adtr.simulation_event_cases FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation();
`

// ImmutableSchemaSQL separately names each protected table for migration review.
const ImmutableSchemaSQL = `
DO $$ DECLARE t text; BEGIN
 FOREACH t IN ARRAY ARRAY['simulation_event_datasets','simulation_events','simulation_event_case_evidence','simulation_event_transitions','simulation_event_receipts'] LOOP
 EXECUTE format('CREATE TRIGGER immutable_simulation_rows BEFORE UPDATE OR DELETE ON adtr.%I FOR EACH ROW EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 EXECUTE format('CREATE TRIGGER immutable_simulation_truncate BEFORE TRUNCATE ON adtr.%I FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_simulation_event_mutation()',t);
 END LOOP;
END $$;
`
