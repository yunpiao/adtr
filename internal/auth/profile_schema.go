package auth

// ProfileSchema is an unregistered migration fragment. The integrating owner
// chooses the global schema version and executes it inside the existing locked
// migration transaction. No runtime path executes DDL.
const ProfileSchema = `
CREATE TABLE adtr.profile_avatars (
 user_id bigint PRIMARY KEY REFERENCES adtr.users(id) ON DELETE CASCADE,
 tenant_id text NOT NULL,
 png bytea NOT NULL CHECK(octet_length(png) BETWEEN 1 AND 4194304),
 updated_at timestamptz NOT NULL
);
`
