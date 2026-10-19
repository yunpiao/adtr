//go:build adtr_ci_dbadmission && (!linux || !amd64)

package dbadmission

import "os"

type ciWitness struct{}

func mapCIWitness(*os.File, [16]byte) (*ciWitness, error) { return nil, errSealProtocol }
func (*ciWitness) invalidate()                            {}
