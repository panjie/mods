package mcpclient

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

func TestInferCapabilities(t *testing.T) {
	t.Run("readOnlyHint true is read-only", func(t *testing.T) {
		caps := inferCapabilities(mcp.Tool{
			Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)},
		})
		require.True(t, caps.ReadOnly, "ReadOnlyHint=true should be ReadOnly")
		require.False(t, caps.Mutable, "ReadOnlyHint=true should not be Mutable")
	})

	t.Run("readOnlyHint false is mutable", func(t *testing.T) {
		caps := inferCapabilities(mcp.Tool{
			Annotations: mcp.ToolAnnotation{ReadOnlyHint: boolPtr(false)},
		})
		require.False(t, caps.ReadOnly)
		require.True(t, caps.Mutable, "explicit ReadOnlyHint=false must degrade to Mutable")
	})

	t.Run("nil hint is mutable (fail-closed)", func(t *testing.T) {
		caps := inferCapabilities(mcp.Tool{})
		require.False(t, caps.ReadOnly)
		require.True(t, caps.Mutable, "absent hint must fail-closed to Mutable")
	})

	t.Run("destructiveHint does not override readOnlyHint", func(t *testing.T) {
		// A server that sets both is contradictory; we honour readOnlyHint
		// since it is the explicit "does not modify" signal.
		caps := inferCapabilities(mcp.Tool{
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint:    boolPtr(true),
				DestructiveHint: boolPtr(true),
			},
		})
		require.True(t, caps.ReadOnly)
	})
}
