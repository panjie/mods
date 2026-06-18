package mcpclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsMCPEnabled(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()

	t.Run("no restrictions", func(t *testing.T) {
		config = Config{}
		require.True(t, isMCPEnabled(&config, "server-a"))
		require.True(t, isMCPEnabled(&config, "server-b"))
	})

	t.Run("whitelist overrides all", func(t *testing.T) {
		config = Config{
			MCPEnable:  []string{"server-a"},
			MCPDisable: []string{"server-a"},
		}
		require.True(t, isMCPEnabled(&config, "server-a"),
			"whitelisted server should be enabled even if also disabled")
		require.False(t, isMCPEnabled(&config, "server-b"),
			"non-whitelisted server should be disabled")
	})

	t.Run("whitelist only", func(t *testing.T) {
		config = Config{
			MCPEnable: []string{"filesystem"},
		}
		require.True(t, isMCPEnabled(&config, "filesystem"))
		require.False(t, isMCPEnabled(&config, "shell"))
		require.False(t, isMCPEnabled(&config, "fetch"))
	})

	t.Run("blacklist specific server", func(t *testing.T) {
		config = Config{
			MCPDisable: []string{"shell"},
		}
		require.True(t, isMCPEnabled(&config, "filesystem"))
		require.True(t, isMCPEnabled(&config, "fetch"))
		require.False(t, isMCPEnabled(&config, "shell"))
	})

	t.Run("blacklist all with star", func(t *testing.T) {
		config = Config{
			MCPDisable: []string{"*"},
		}
		require.False(t, isMCPEnabled(&config, "filesystem"))
		require.False(t, isMCPEnabled(&config, "shell"))
		require.False(t, isMCPEnabled(&config, "fetch"))
	})
}
