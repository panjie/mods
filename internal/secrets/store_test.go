package secrets

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeCredentialRedaction(t *testing.T) {
	store := New()
	store.RememberForRedaction("")
	store.RememberForRedaction("user")
	secret := "user-token\\\"<&>"
	store.RememberForRedaction(secret)
	store.RememberForRedaction(secret)
	require.Empty(t, store.entries, "runtime credentials must not create resolvable references")
	require.Len(t, store.redactions, 2)
	require.Equal(t, "[REDACTED]", store.Redact(secret))
	encoded, err := json.Marshal(map[string]string{"stdout": secret})
	require.NoError(t, err)
	require.JSONEq(t, `{"stdout":"[REDACTED]"}`, store.Redact(string(encoded)))
	buf := store.redactions[1]
	store.Clear()
	require.Empty(t, store.redactions)
	require.Equal(t, make([]byte, len(buf)), buf)
	require.Equal(t, secret, store.Redact(secret))
}

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
