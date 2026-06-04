package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewConversationID(t *testing.T) {
	id1 := newConversationID()
	id2 := newConversationID()

	require.Len(t, id1, 40)
	require.Len(t, id2, 40)
	require.NotEqual(t, id1, id2)
	require.True(t, sha1reg.MatchString(id1))
	require.True(t, sha1reg.MatchString(id2))
}
