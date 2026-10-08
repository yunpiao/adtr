package auth

// DirectoryPermissionMarks is the migration-15 permission prerequisite. It grants
// no permissions, changes no registry, and does not enable a directory consumer.
// credentialuse.DirectoryPurposeSchema owns the directory permission epoch
// trigger; installing a second trigger here would double-bump authorization.
const DirectoryPermissionMarks = `
ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks','audit','audit_exports','system','schedules','task_archive','domains','operation_accounts','system_logs','directory_assets'));
`
