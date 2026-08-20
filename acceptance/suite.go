package acceptance

import (
	"context"
	"net/http/httptest"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/suite"

	acceptconfig "github.com/mohamadhallal/zentax-api/acceptance/config"
	"github.com/mohamadhallal/zentax-api/bootstrap"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
	"github.com/mohamadhallal/zentax-api/logger"
)

const defaultDatabaseURL = "postgres://admin:secret@postgres:5432/golang-api?sslmode=disable"

// Suite is the base acceptance test suite. Embed it in module-level suites.
type Suite struct {
	suite.Suite

	DB       *sqlx.DB
	dbURL    string
	External *httptest.Server
	Internal *httptest.Server
	Client   *TestClient
	extApp   *bootstrap.App
	intApp   *bootstrap.App
}

func (s *Suite) SetupSuite() {
	logger.InitBasic()

	s.dbURL = os.Getenv("TEST_DATABASE_URL")
	if s.dbURL == "" {
		s.dbURL = defaultDatabaseURL
	}

	var err error
	s.DB, err = sqlx.Open("pgx", s.dbURL)
	s.Require().NoError(err)
	s.Require().NoError(s.DB.PingContext(context.Background()))
}

func (s *Suite) TearDownSuite() {
	if s.DB != nil {
		s.DB.Close()
	}
}

func (s *Suite) SetupTest() {
	cfg := acceptconfig.DefaultConfig()
	cfg.Database.URL = s.dbURL

	var err error
	s.extApp, err = bootstrap.New(cfg, types.ModeExternal)
	s.Require().NoError(err)
	s.External = httptest.NewServer(s.extApp.Router)

	s.intApp, err = bootstrap.New(cfg, types.ModeInternal)
	s.Require().NoError(err)
	s.Internal = httptest.NewServer(s.intApp.Router)

	s.Client = NewTestClient(s.External.URL, s.Internal.URL)
}

func (s *Suite) TearDownTest() {
	if s.External != nil {
		s.External.Close()
	}
	if s.Internal != nil {
		s.Internal.Close()
	}
	if s.extApp != nil {
		s.Require().NoError(s.extApp.Close())
	}
	if s.intApp != nil {
		s.Require().NoError(s.intApp.Close())
	}
	s.TruncateTables()
	s.External = nil
	s.Internal = nil
	s.extApp = nil
	s.intApp = nil
	s.Client = nil
}
