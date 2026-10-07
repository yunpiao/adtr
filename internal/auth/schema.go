package auth

// Schema is executed by the explicit, versioned infrastructure migrator.
// Runtime authentication never creates tables.
const Schema = `
CREATE TABLE IF NOT EXISTS adtr.users (
 id bigserial PRIMARY KEY, tenant_id text NOT NULL,
 username text NOT NULL UNIQUE CHECK (length(username) BETWEEN 1 AND 64),
 password_hash text NOT NULL, pass_strength text NOT NULL DEFAULT 'low' CHECK (pass_strength IN ('high','middle','low')), role text NOT NULL CHECK (role IN ('platform_admin','viewer')),
 mobile text NOT NULL DEFAULT '', email text NOT NULL DEFAULT '', remark text NOT NULL DEFAULT '',
 must_change boolean NOT NULL DEFAULT true, disabled boolean NOT NULL DEFAULT false,
 password_updated_at timestamptz NOT NULL DEFAULT now(),
 mfa_secret text NOT NULL DEFAULT '', mfa_pending text NOT NULL DEFAULT '',
 mfa_pending_until timestamptz, mfa_last_step bigint NOT NULL DEFAULT -1,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS adtr.sessions (
 token_hash text PRIMARY KEY, user_id bigint NOT NULL REFERENCES adtr.users(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user ON adtr.sessions(user_id);
CREATE TABLE IF NOT EXISTS adtr.auth_attempts (
 bucket text PRIMARY KEY, count integer NOT NULL, window_start timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS adtr.auth_audit (
 id bigserial PRIMARY KEY, actor_id bigint, tenant_id text NOT NULL,
 action text NOT NULL, target_id bigint, occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE OR REPLACE FUNCTION adtr.reject_audit_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'audit records are append-only'; END;
$$;
DROP TRIGGER IF EXISTS auth_audit_immutable ON adtr.auth_audit;
CREATE TRIGGER auth_audit_immutable BEFORE UPDATE OR DELETE ON adtr.auth_audit
FOR EACH ROW EXECUTE FUNCTION adtr.reject_audit_mutation();
DROP TRIGGER IF EXISTS auth_audit_no_truncate ON adtr.auth_audit;
CREATE TRIGGER auth_audit_no_truncate BEFORE TRUNCATE ON adtr.auth_audit
FOR EACH STATEMENT EXECUTE FUNCTION adtr.reject_audit_mutation();
`
