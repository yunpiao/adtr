package auth

const SystemPermissionMarks = `
ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks','audit','audit_exports','system'));
`
