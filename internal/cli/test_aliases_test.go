package cli

import (
	"os"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/conversation"
	"github.com/stretchr/testify/require"
)

type PersistentConfig = cfgpkg.PersistentConfig

func testDB(tb testing.TB) *conversation.DB {
	db, err := conversation.Open(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}

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
