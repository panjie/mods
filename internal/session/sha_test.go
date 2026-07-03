package session

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewSessionID(t *testing.T) {
	id1 := newSessionID()
	id2 := newSessionID()

	require.Len(t, id1, 40)
	require.Len(t, id2, 40)
	require.NotEqual(t, id1, id2)
	require.True(t, sha1reg.MatchString(id1))
	require.True(t, sha1reg.MatchString(id2))
}
