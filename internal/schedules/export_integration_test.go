//go:build integration

package schedules

// The external integration suite can install the full production migrator
// without introducing a schedules -> store import cycle. These aliases expose
// only its existing synthetic seeding and cleanup helpers to that test binary.
const IntegrationColumns = columns

var (
	IntegrationValidCreate     = validCreate
	IntegrationGridAt          = gridAt
	IntegrationHash            = hash
	IntegrationScan            = scan
	IntegrationCloseConnection = closeConnection
	IntegrationRollback        = rollback
)
