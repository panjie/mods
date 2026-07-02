package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorizedDirsRoundTrip(t *testing.T) {
	ctx := context.Background()
	require.Nil(t, AuthorizedDirs(ctx))
	ctx = WithAuthorizedDirs(ctx, []string{"/a", "/b"})
	require.Equal(t, []string{"/a", "/b"}, AuthorizedDirs(ctx))
}

func TestAuthorizedDirsDoesNotMutateOriginal(t *testing.T) {
	ctx := context.Background()
	derived := WithAuthorizedDirs(ctx, []string{"/a"})
	require.Nil(t, AuthorizedDirs(ctx), "parent ctx must not carry dirs")
	require.Equal(t, []string{"/a"}, AuthorizedDirs(derived))
}

func TestAuthorizedDirsEmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	same := WithAuthorizedDirs(ctx, nil)
	require.True(t, same == ctx, "empty slice must return ctx unchanged")
}
