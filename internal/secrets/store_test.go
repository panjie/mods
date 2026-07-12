package secrets

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreResolveIsBoundAndRedacted(t *testing.T) {
	store := New()
	ref, err := store.Put("super-secret", Target{Tool: "db_query", Path: "/password"})
	require.NoError(t, err)
	require.True(t, IsRef(ref))

	resolved, used, err := store.Resolve("db_query", []byte(`{"password":"`+ref+`","query":"select 1"}`))
	require.NoError(t, err)
	require.True(t, used)
	require.JSONEq(t, `{"password":"super-secret","query":"select 1"}`, string(resolved))

	_, _, err = store.Resolve("other_tool", []byte(`{"password":"`+ref+`"}`))
	require.Error(t, err)
	_, _, err = store.Resolve("db_query", []byte(`{"other":"`+ref+`"}`))
	require.Error(t, err)
	require.Equal(t, "value=[REDACTED]", store.Redact("value=super-secret"))

	store.Clear()
	_, _, err = store.Resolve("db_query", []byte(`{"password":"`+ref+`"}`))
	require.Error(t, err)
}

func TestStoreDoesNotInterpolateReferences(t *testing.T) {
	store := New()
	ref, err := store.Put("secret", Target{Tool: "shell_run", Path: "/secret_env/PASSWORD"})
	require.NoError(t, err)
	data := []byte(`{"command":"echo ` + ref + `"}`)
	resolved, used, err := store.Resolve("shell_run", data)
	require.NoError(t, err)
	require.False(t, used)
	require.Equal(t, data, resolved)
}
