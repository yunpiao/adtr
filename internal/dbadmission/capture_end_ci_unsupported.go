//go:build adtr_ci_dbadmission && (!linux || !amd64)

package dbadmission

import "os"

func prepareCaptureEnd(*os.File) (int, error)                { return 0, errSealProtocol }
func (g *lifecycleGate) captureObligationsEndedLocked() bool { return false }
