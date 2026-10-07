package auth

// ResourceSchema follows SchemaV3. It is run only by the explicit migrator.
// A domain catalogue is empty until a separately authorized AD connection module
// registers an owned domain; this migration never invents available inventory.
const ResourceSchema = `
CREATE TABLE adtr.resource_tenant_config (
 tenant_id text PRIMARY KEY,
 max_ad_count integer NOT NULL CHECK(max_ad_count BETWEEN 0 AND 100000),
 expire_time bigint NOT NULL CHECK(expire_time BETWEEN 0 AND 253402300799),
 uid text NOT NULL CHECK(length(uid) BETWEEN 1 AND 256),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 32),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE adtr.resource_domains (
 tenant_id text NOT NULL, id text NOT NULL, name text NOT NULL,
 active boolean NOT NULL DEFAULT true,
 PRIMARY KEY(tenant_id,id), CHECK(length(id) BETWEEN 1 AND 128), CHECK(length(name)>0)
);
CREATE TABLE adtr.resource_groups (
 tenant_id text NOT NULL, id text NOT NULL, name text NOT NULL,
 mark text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,id), CHECK(octet_length(name) BETWEEN 1 AND 256), CHECK(length(mark)<=500)
);
CREATE UNIQUE INDEX resource_groups_tenant_name ON adtr.resource_groups(tenant_id,lower(name));
CREATE TABLE adtr.resource_group_members (
 tenant_id text NOT NULL, group_id text NOT NULL, domain_id text NOT NULL,
 PRIMARY KEY(tenant_id,group_id,domain_id),
 FOREIGN KEY(tenant_id,group_id) REFERENCES adtr.resource_groups(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,domain_id) REFERENCES adtr.resource_domains(tenant_id,id) ON DELETE CASCADE
);
CREATE TABLE adtr.resource_role_groups (
 tenant_id text NOT NULL, role_id text NOT NULL, group_id text NOT NULL,
 custom_role_id text GENERATED ALWAYS AS (CASE WHEN role_id IN ('platform_admin','viewer') THEN NULL ELSE role_id END) STORED,
 PRIMARY KEY(tenant_id,role_id,group_id),
 FOREIGN KEY(tenant_id,group_id) REFERENCES adtr.resource_groups(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY(tenant_id,custom_role_id) REFERENCES adtr.access_roles(tenant_id,id) ON DELETE CASCADE
);
CREATE INDEX resource_role_groups_by_group ON adtr.resource_role_groups(tenant_id,group_id);
CREATE INDEX resource_group_members_by_domain ON adtr.resource_group_members(tenant_id,domain_id);
CREATE TABLE adtr.resource_audit (
 id bigserial PRIMARY KEY, actor_id bigint NOT NULL, tenant_id text NOT NULL,
 action text NOT NULL, target_id text NOT NULL,
 occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE TRIGGER resource_audit_immutable BEFORE UPDATE OR DELETE ON adtr.resource_audit
FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
CREATE TRIGGER resource_audit_no_truncate BEFORE TRUNCATE ON adtr.resource_audit
FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
