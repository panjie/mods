package testutil

import (
	"testing"

	"github.com/panjie/mods/internal/session"
	"github.com/stretchr/testify/require"
)

func OpenTestDB(tb testing.TB) *session.DB {
	db, err := session.Open(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}
