package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsMCPEnabled(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()

	t.Run("no restrictions", func(t *testing.T) {
		config = Config{}
		require.True(t, isMCPEnabled("server-a"))
		require.True(t, isMCPEnabled("server-b"))
	})

	t.Run("whitelist overrides all", func(t *testing.T) {
		config = Config{
			MCPEnable:  []string{"server-a"},
			MCPDisable: []string{"server-a"},
		}
		require.True(t, isMCPEnabled("server-a"),
			"whitelisted server should be enabled even if also disabled")
		require.False(t, isMCPEnabled("server-b"),
			"non-whitelisted server should be disabled")
	})

	t.Run("whitelist only", func(t *testing.T) {
		config = Config{
			MCPEnable: []string{"filesystem"},
		}
		require.True(t, isMCPEnabled("filesystem"))
		require.False(t, isMCPEnabled("shell"))
		require.False(t, isMCPEnabled("fetch"))
	})

	t.Run("blacklist specific server", func(t *testing.T) {
		config = Config{
			MCPDisable: []string{"shell"},
		}
		require.True(t, isMCPEnabled("filesystem"))
		require.True(t, isMCPEnabled("fetch"))
		require.False(t, isMCPEnabled("shell"))
	})

	t.Run("blacklist all with star", func(t *testing.T) {
		config = Config{
			MCPDisable: []string{"*"},
		}
		require.False(t, isMCPEnabled("filesystem"))
		require.False(t, isMCPEnabled("shell"))
		require.False(t, isMCPEnabled("fetch"))
	})
}
