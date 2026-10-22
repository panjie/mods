package session

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaVersionFastPathAndFutureVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.db")
	db, err := Open(path)
	require.NoError(t, err)
	var version int
	require.NoError(t, db.db.Get(&version, "PRAGMA user_version"))
	require.Equal(t, schemaVersion, version)
	require.NoError(t, db.Save("test-id", "test title", "api", "model"))
	require.NoError(t, db.Close())
	// A read-only reopen succeeds because the current schema needs no DDL.
	db, err = Open("file:" + filepath.ToSlash(path) + "?mode=ro")
	require.NoError(t, err)
	found, err := db.Find("test-id")
	require.NoError(t, err)
	require.Equal(t, "test title", found.Title)
	require.NoError(t, db.Close())
	raw, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer raw.Close()
	_, err = raw.Exec("PRAGMA user_version = 999")
	require.NoError(t, err)
	db, err = Open(path)
	require.ErrorContains(t, err, "newer than supported")
	require.Nil(t, db)
}
