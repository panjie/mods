package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

func TestMakeGradientRamp(t *testing.T) {
	t.Run("length 2", func(t *testing.T) {
		ramp := makeGradientRamp(2)
		require.Len(t, ramp, 2)
		require.NotEmpty(t, ramp[0])
		require.NotEmpty(t, ramp[1])
	})

	t.Run("length 5", func(t *testing.T) {
		ramp := makeGradientRamp(5)
		require.Len(t, ramp, 5)
	})

	t.Run("length 0 returns empty", func(t *testing.T) {
		ramp := makeGradientRamp(0)
		require.Empty(t, ramp)
	})
}

func TestReverse(t *testing.T) {
	t.Run("ints", func(t *testing.T) {
		require.Equal(t, []int{3, 2, 1}, reverse([]int{1, 2, 3}))
	})
	t.Run("strings", func(t *testing.T) {
		require.Equal(t, []string{"c", "b", "a"}, reverse([]string{"a", "b", "c"}))
	})
	t.Run("single element", func(t *testing.T) {
		require.Equal(t, []int{1}, reverse([]int{1}))
	})
	t.Run("empty", func(t *testing.T) {
		require.Empty(t, reverse([]int{}))
	})
}

func TestMakeGradientText(t *testing.T) {
	baseStyle := lipgloss.NewStyle()

	t.Run("single char returns unchanged", func(t *testing.T) {
		result := makeGradientText(baseStyle, "a")
		require.Equal(t, "a", result)
	})

	t.Run("empty returns empty", func(t *testing.T) {
		result := makeGradientText(baseStyle, "")
		require.Empty(t, result)
	})

	t.Run("multi-char returns styled text", func(t *testing.T) {
		result := makeGradientText(baseStyle, "abc")
		require.NotEmpty(t, result)
	})
}
