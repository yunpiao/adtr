package auth

// TaskPermissionSchema is the explicit version-5 extension of the closed
// function permission registry. Unknown marks remain rejected by PostgreSQL.
const TaskPermissionSchema = `ALTER TABLE adtr.access_permissions DROP CONSTRAINT access_permissions_mark_check;
ALTER TABLE adtr.access_permissions ADD CONSTRAINT access_permissions_mark_check CHECK(mark IN ('users','roles','permissions','tasks'));`
