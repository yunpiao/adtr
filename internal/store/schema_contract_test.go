package store

import (
	"github.com/yunpiao/adtr/internal/audit"
	"github.com/yunpiao/adtr/internal/operationallogs"
	"github.com/yunpiao/adtr/internal/schedules"
	"github.com/yunpiao/adtr/internal/schemaversion"
	"github.com/yunpiao/adtr/internal/taskarchive"
	"testing"
)

func TestApplicationModulesAgreeOnExactSchemaVersion(t *testing.T) {
	if operationallogs.SchemaVersion != SchemaVersion || SchemaVersion != schemaversion.Current || audit.SchemaVersion != SchemaVersion || schedules.SchemaVersion != SchemaVersion || taskarchive.SchemaVersion != SchemaVersion {
		t.Fatal("API module and migrator schema contracts diverged")
	}
}
