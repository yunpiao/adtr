package domains

const DirectoryDependencyV3Schema = `
ALTER TABLE adtr.operation_account_dependencies DROP CONSTRAINT operation_account_dependency_pins;
ALTER TABLE adtr.operation_account_dependencies ADD CONSTRAINT operation_account_dependency_pins CHECK(
 (consumer_kind IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read','domain.directory_read.v2','domain.directory_read.v3') AND account_credential_revision IS NOT NULL AND account_credential_revision>0 AND connection_credential_generation IS NOT NULL AND connection_credential_generation>0)
 OR (consumer_kind NOT IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read','domain.directory_read.v2','domain.directory_read.v3') AND account_credential_revision IS NULL AND connection_credential_generation IS NULL));
DROP INDEX adtr.domain_directory_versioned_use_identity;
CREATE UNIQUE INDEX domain_directory_versioned_use_identity ON adtr.operation_account_dependencies(object_id) WHERE consumer_kind IN ('domain.directory_read','domain.directory_read.v2','domain.directory_read.v3');
CREATE OR REPLACE FUNCTION adtr.domain_typed_dependency_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND (OLD.consumer_kind IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read','domain.directory_read.v2','domain.directory_read.v3') OR NEW.consumer_kind IN ('domain.connection_binding','domain.account_connection_test','domain.directory_read','domain.directory_read.v2','domain.directory_read.v3')) AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'typed dependency identity is immutable' USING ERRCODE='42501'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;ELSE RETURN NEW;END IF;
END; $$;
`
