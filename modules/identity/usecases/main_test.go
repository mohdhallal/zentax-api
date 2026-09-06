package usecases

import (
	"os"
	"testing"

	"github.com/mohamadhallal/zentax-api/logger"
)

// The login path logs (at Error) when the failed-attempt counter cannot be
// written, so the package-level logger must exist for the tests that force it.
func TestMain(m *testing.M) {
	logger.InitBasic()
	os.Exit(m.Run())
}
