package ratelimit

import (
	"os"
	"testing"

	"github.com/mohamadhallal/zentax-api/logger"
)

func TestMain(m *testing.M) {
	logger.InitBasic()
	os.Exit(m.Run())
}
