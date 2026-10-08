package domains

// DirectoryDependencySchema is the migration-15 extension of the existing typed
// dependency boundary. Install atomically with DirectoryUseSchema, never alone.
// Unknown pre-existing uses of the reserved kind are rejected, not adopted.
const DirectoryDependencySchema = `
DO $$ BEGIN
 IF EXISTS(SELECT FROM adtr.operation_account_dependencies WHERE consumer_kind='domain.directory_read') THEN
  RAISE EXCEPTION 'reserved directory dependency kind already exists' USING ERRCODE='23514',CONSTRAINT='domain_directory_dependency_kind_reserved';
 END IF;
END; $$;
ALTER TABLE adtr.operation_account_dependencies DROP CONSTRAINT operation_account_dependency_pins;
ALTER TABLE adtr.operation_account_dependencies ADD CONSTRAINT operation_account_dependency_pins CHECK(
 (consumer_kind IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read') AND account_credential_revision IS NOT NULL AND account_credential_revision>0 AND connection_credential_generation IS NOT NULL AND connection_credential_generation>0)
 OR (consumer_kind NOT IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read') AND account_credential_revision IS NULL AND connection_credential_generation IS NULL));
CREATE UNIQUE INDEX domain_directory_use_identity ON adtr.operation_account_dependencies(object_id) WHERE consumer_kind='domain.directory_read';
CREATE OR REPLACE FUNCTION adtr.domain_typed_dependency_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (OLD.consumer_kind IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read') OR NEW.consumer_kind IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read')) AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'typed dependency identity is immutable' USING ERRCODE='42501'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
END; $$;
`
