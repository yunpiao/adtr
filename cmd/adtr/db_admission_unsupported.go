//go:build adtr_ci_dbadmission && (!linux || !amd64)

package main

import (
	"errors"
	"os"
)

func configureCIAdmission(_ string) (<-chan struct{}, func(), error) {
	for _, key := range []string{"ADTR_CI_DB_NORMAL_FD", "ADTR_CI_DB_RESCUE_FD", "ADTR_CI_DB_WITNESS_FD", "ADTR_CI_CAPTURE_END_FD"} {
		if _, ok := os.LookupEnv(key); ok {
			return nil, nil, errors.New("CI database controller unavailable")
		}
	}
	return nil, func() {}, nil
}
