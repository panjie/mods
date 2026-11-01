package cli

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Bubble Tea erases and resizes its inline buffer when Mods.View changes to
// an empty view at completion. The CLI then prints the final answer. Losing
// the old cursor row during that shrink leaves the live answer on screen,
// making the final print look like a duplicate model response.
func TestTurnRendererClearsLiveOutputBeforeFinalPrint(t *testing.T) {
	for _, height := range []int{2, 4, 24} {
		live := strings.Repeat("锁屏时间：5 分钟\n", height-1) + "Generating"
		t.Run(strconv.Itoa(height)+" rows", func(t *testing.T) {
			var output bytes.Buffer
			renderer := uv.NewTerminalRenderer(&output, []string{"TERM=xterm-256color"})
			renderer.SetColorProfile(colorprofile.Ascii)
			renderer.SetRelativeCursor(true)
			renderer.Resize(80, 24)
			buffer := uv.NewScreenBuffer(80, lipgloss.Height(live))
			uv.NewStyledString(live).Draw(buffer, buffer.Bounds())
			renderer.Render(buffer.RenderBuffer)
			require.NoError(t, renderer.Flush())
			_, row := renderer.Position()
			require.Equal(t, height-1, row)
			output.Reset()

			// Mirror Bubble Tea's frame resize and forced redraw, including
			// the one physical row occupied by an empty styled string.
			renderer.Erase()
			buffer.Resize(80, 1)
			buffer.Clear()
			renderer.Render(buffer.RenderBuffer)
			require.NoError(t, renderer.Flush())

			// Clearing from the old bottom row only erases the spinner.
			// The cursor must first return to the top of the live answer.
			clear := output.String()
			up := strings.Index(clear, ansi.CursorUp(height-1))
			erase := strings.Index(clear, ansi.EraseScreenBelow)
			require.GreaterOrEqual(t, up, 0, "missing cursor-up before clear: %q", clear)
			require.Greater(t, erase, up, "must move up before erasing: %q", clear)
		})
	}
}
