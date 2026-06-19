package cli

import (
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/conversation"
	"github.com/stretchr/testify/require"
	"testing"
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
