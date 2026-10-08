package auth

// SchemaV3 extends local identity with tenant-scoped custom function roles.
// Builtin role IDs are server-owned definitions, never writable role records.
const SchemaV3 = `
CREATE TABLE adtr.access_roles (
 tenant_id text NOT NULL, id text NOT NULL, name text NOT NULL,
 remark text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,id), CHECK(length(name) BETWEEN 1 AND 50),
 CHECK(id NOT IN ('platform_admin','viewer')), CHECK(length(remark)<=150)
);
CREATE UNIQUE INDEX access_roles_tenant_name ON adtr.access_roles(tenant_id,lower(name));
CREATE TABLE adtr.access_permissions (
 tenant_id text NOT NULL, role_id text NOT NULL, mark text NOT NULL,
 readable boolean NOT NULL, writeable boolean NOT NULL,
 PRIMARY KEY(tenant_id,role_id,mark),
 FOREIGN KEY(tenant_id,role_id) REFERENCES adtr.access_roles(tenant_id,id) ON DELETE CASCADE,
 CHECK(mark IN ('users','roles','permissions')), CHECK(NOT writeable OR readable)
);
ALTER TABLE adtr.users ADD COLUMN role_id text NOT NULL DEFAULT '';
ALTER TABLE adtr.users ADD COLUMN custom_role_id text GENERATED ALWAYS AS (NULLIF(role_id,'')) STORED;
ALTER TABLE adtr.users ADD CONSTRAINT users_custom_role FOREIGN KEY(tenant_id,custom_role_id)
 REFERENCES adtr.access_roles(tenant_id,id) ON DELETE RESTRICT;
ALTER TABLE adtr.users ADD CONSTRAINT users_custom_role_base CHECK(role_id='' OR role='viewer');
ALTER TABLE adtr.users ADD COLUMN address text NOT NULL DEFAULT '';
ALTER TABLE adtr.users ADD COLUMN real_name text NOT NULL DEFAULT '';
ALTER TABLE adtr.users ADD COLUMN department text NOT NULL DEFAULT '';
ALTER TABLE adtr.users ADD COLUMN post text NOT NULL DEFAULT '';
CREATE INDEX users_tenant_role ON adtr.users(tenant_id,role_id,role);
`
