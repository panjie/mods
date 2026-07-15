package cli

import (
	"os"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/testutil"
)

type PersistentConfig = cfgpkg.PersistentConfig
type APIs = cfgpkg.APIs

var testDB = testutil.OpenTestDB

func withTestConfig(t *testing.T, cfg Config, fn func()) {
	t.Helper()
	saveConfig := config
	config = cfg
	defer func() { config = saveConfig }()
	fn()
}

func TestMain(m *testing.M) {
	ensureTestFlags()
	os.Exit(m.Run())
}
