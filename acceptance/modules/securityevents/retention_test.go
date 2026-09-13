package securityevents_test

import (
	"os"

	acceptconfig "github.com/mohamadhallal/zentax-api/acceptance/config"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// TestRetentionIsRegisteredAsScheduledWork closes the gap this wave was about.
// The thirteen-month window was stated as an implemented control in three
// places — ADR-0007, the security_events table comment, and the operations
// runbook, which tells an operator to "pull what you need early: it is the one
// with the short window" — and nothing dropped anything. What made that worse
// than an undocumented gap is that the record is what stops anybody looking.
//
// So the wiring is the claim worth testing, and it is a different claim from
// "the function drops the right partition" (proved in
// platform/securityevent/retention_pg_test.go, against Postgres, as the app
// role): a control that works perfectly and is never scheduled is exactly the
// state the product was already in. The composition root must register it when
// the deployment has retention on, and must leave it out when a deployment has
// deliberately switched it off.
//
// The scheduler is BUILT by bootstrap.New and started only by cmd/server, so
// nothing here starts background work, contends for leadership, or drops a
// partition out from under the suite.
func (s *SecuritySuite) TestRetentionIsRegisteredAsScheduledWork() {
	on := s.appWithRetention(true)
	defer on.close()
	s.Require().Contains(on.app.Scheduler.Jobs(), securityevent.JobName,
		"the thirteen-month window must be scheduled work, not a sentence in a runbook")

	off := s.appWithRetention(false)
	defer off.close()
	s.Require().NotContains(off.app.Scheduler.Jobs(), securityevent.JobName,
		"a deployment that switched retention off must not drop partitions anyway")
}

// TestTheRegisteredWindowIsTheDecidedOne. Which window is running is the whole
// content of the control: an environment that says nothing about retention must
// still get thirteen months, because a retention promise that only holds where
// somebody remembered to configure it is one the next cell ships without.
func (s *SecuritySuite) TestTheRegisteredWindowIsTheDecidedOne() {
	var cfg config.SecurityEventsConfig
	cfg.ApplyDefaults()

	s.Require().True(cfg.RetentionActive(),
		"an omitted securityEvents section must still run retention")
	s.Require().Equal(13, cfg.RetentionMonths,
		"ADR-0007 fixed thirteen months: a SOC 2 audit period plus a month of overlap")
	s.Require().GreaterOrEqual(cfg.RetentionMonths, config.MinSecurityEventsRetentionMonths)
}

type builtRetentionApp struct {
	app   *bootstrap.App
	close func()
}

func (s *SecuritySuite) appWithRetention(on bool) builtRetentionApp {
	cfg := acceptconfig.DefaultConfig()
	cfg.Database.URL = os.Getenv("TEST_DATABASE_URL")
	if cfg.Database.URL == "" {
		cfg.Database.URL = "postgres://admin:secret@postgres:5432/golang-api?sslmode=disable"
	}
	cfg.SecurityEvents = config.SecurityEventsConfig{RetentionEnabled: &on}
	cfg.SecurityEvents.ApplyDefaults()

	app, err := bootstrap.New(cfg, types.ModeExternal)
	s.Require().NoError(err)
	return builtRetentionApp{app: app, close: func() {
		s.Require().NoError(app.Close())
		_ = os.RemoveAll(cfg.Storage.FS.Root)
	}}
}
