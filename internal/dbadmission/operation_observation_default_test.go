//go:build !adtr_ci_dbadmission

package dbadmission

import (
	"github.com/jackc/pgx/v5"
	"testing"
)

func TestDefaultOperationObserverIsExactPassThrough(t *testing.T) {
	config, err := pgx.ParseConfig("postgres://localhost/test")
	if err != nil {
		t.Fatal(err)
	}
	if observeLifecycleConfig(config, nil) != config || config.Tracer != nil {
		t.Fatal("default observer changed config")
	}
}
