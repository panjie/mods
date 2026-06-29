package testutil

import (
	"testing"

	"github.com/panjie/mods/internal/conversation"
	"github.com/stretchr/testify/require"
)

func OpenTestDB(tb testing.TB) *conversation.DB {
	db, err := conversation.Open(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}
