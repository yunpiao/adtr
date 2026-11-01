//go:build integration

package auth

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Reduced fixtures exercise today's role writer without installing unrelated
// production migrations. Their final CHECK must accept every current mark,
// including explicit deny rows; broadening the vocabulary must not grant rights.
func TestCurrentAuthFixturePermissionVocabulary(t *testing.T) {
	fixtures := []struct {
		name string
		open func(*testing.T) (context.Context, *pgx.Conn)
	}{
		{"access", func(t *testing.T) (context.Context, *pgx.Conn) { f := newAccessFixture(t); return f.ctx, f.conn }},
		{"resource", func(t *testing.T) (context.Context, *pgx.Conn) { f := newResourceFixture(t); return f.ctx, f.conn }},
		{"audit", func(t *testing.T) (context.Context, *pgx.Conn) { f := newAuditFixture(t); return f.ctx, f.conn }},
		{"domain", func(t *testing.T) (context.Context, *pgx.Conn) { f := newDomainFixture(t); return f.ctx, f.conn }},
		{"schedule", func(t *testing.T) (context.Context, *pgx.Conn) { f := newScheduleFixture(t); return f.ctx, f.conn }},
		{"archive", func(t *testing.T) (context.Context, *pgx.Conn) { f := newArchiveFixture(t); return f.ctx, f.conn }},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, conn := fixture.open(t)
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			const tenant, role = "fixture-vocabulary", "vvvvvvvvvvvvvvvvvvvvvvvv"
			if _, err = tx.Exec(ctx, "INSERT INTO adtr.access_roles(tenant_id,id,name) VALUES($1,$2,'Vocabulary regression')", tenant, role); err != nil {
				t.Fatal(err)
			}
			if err = storeAccessGrants(ctx, tx, tenant, role, map[string]AccessAuth{}); err != nil {
				t.Fatalf("current role writer rejected by fixture vocabulary: %v", err)
			}
			var total, granted int
			if err = tx.QueryRow(ctx, "SELECT count(*),count(*) FILTER (WHERE readable OR writeable) FROM adtr.access_permissions WHERE tenant_id=$1 AND role_id=$2", tenant, role).Scan(&total, &granted); err != nil {
				t.Fatal(err)
			}
			if total != len(accessMarks) || granted != 0 {
				t.Fatalf("explicit deny rows: total=%d want=%d granted=%d", total, len(accessMarks), granted)
			}
		})
	}
}
