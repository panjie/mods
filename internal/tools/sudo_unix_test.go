//go:build !windows

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareSudoCommandNonInteractive(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo rm /tmp/example", nil)
	defer cleanup()
	require.NoError(t, err)
	require.Contains(t, prepared.Command, "sudo -n rm")
	require.Empty(t, prepared.Env)
}

func TestPrepareSudoCommandInteractive(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo -u root rm /tmp/example", func(context.Context, string, string) (string, error) {
		return "password", nil
	})
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skip("sandbox does not permit Unix-domain listeners")
	}
	require.NoError(t, err)
	defer cleanup()
	require.Contains(t, prepared.Command, "sudo -A -u root rm")
	require.NotEmpty(t, prepared.Env["SUDO_ASKPASS"])
}

func TestPrepareSudoCommandModes(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo -n true", nil)
	defer cleanup()
	require.NoError(t, err)
	require.Equal(t, "sudo -n true", prepared.Command)

	_, cleanup, err = prepareSudoCommand(context.Background(), "printf x | sudo -S cat", nil)
	defer cleanup()
	require.ErrorContains(t, err, "sudo -S")

	_, cleanup, err = prepareSudoCommand(context.Background(), `sh -c "sudo true"`, nil)
	defer cleanup()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "nested") || strings.Contains(err.Error(), "parsed safely"))
}
