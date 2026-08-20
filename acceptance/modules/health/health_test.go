package health_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type HealthSuite struct {
	acceptance.Suite
}

func TestHealthSuite(t *testing.T) {
	suite.Run(t, new(HealthSuite))
}

func (s *HealthSuite) TestHealthcheck_Returns200() {
	resp := s.Client.External().GET(s.T(), "/health")
	resp.AssertStatus(s.T(), http.StatusOK)
}
