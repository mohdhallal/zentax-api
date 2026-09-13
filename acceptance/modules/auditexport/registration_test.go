package auditexport_test

import (
	"os"

	acceptconfig "github.com/mohamadhallal/zentax-api/acceptance/config"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/audit/worm"
)

// databaseURL is the suite's own connection string, read the same way
// acceptance.Suite reads it.
const defaultDatabaseURL = "postgres://admin:secret@postgres:5432/golang-api?sslmode=disable"

func databaseURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return defaultDatabaseURL
}

// TestTheExportIsRegisteredAsScheduledWork closes the ADR-0008 gap that read
// "nothing ever runs the verifier". A job nobody registered is a job nobody
// runs, so the wiring itself is a claim worth testing: the composition root
// must put the export in the scheduler when the deployment has it on, and must
// leave it out when the deployment has switched it off.
//
// The scheduler is BUILT by bootstrap.New and started only by cmd/server, so
// nothing here starts background work or contends for leadership.
func (s *AuditExportSuite) TestTheExportIsRegisteredAsScheduledWork() {
	enabled := s.appWithExport(true)
	defer enabled.close()
	s.Require().Contains(enabled.app.Scheduler.Jobs(), worm.JobName,
		"the WORM export must be scheduled work, not something an operator remembers to run")

	disabled := s.appWithExport(false)
	defer disabled.close()
	s.Require().NotContains(disabled.app.Scheduler.Jobs(), worm.JobName,
		"a deployment that switched the export off must not run it anyway")
}

type builtApp struct {
	app   *bootstrap.App
	close func()
}

func (s *AuditExportSuite) appWithExport(on bool) builtApp {
	cfg := acceptconfig.DefaultConfig()
	cfg.Database.URL = databaseURL()
	cfg.AuditExport = config.AuditExportConfig{Enabled: &on}
	cfg.AuditExport.ApplyDefaults()

	app, err := bootstrap.New(cfg, types.ModeExternal)
	s.Require().NoError(err)
	return builtApp{app: app, close: func() {
		s.Require().NoError(app.Close())
		_ = os.RemoveAll(cfg.Storage.FS.Root)
	}}
}
