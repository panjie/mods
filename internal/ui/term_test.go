package ui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaticBackgroundIsDark(t *testing.T) {
	tests := map[string]struct {
		colorFGBG string
		want      bool
	}{
		"missing defaults dark":   {colorFGBG: "", want: true},
		"malformed defaults dark": {colorFGBG: "light", want: true},
		"black background":        {colorFGBG: "15;0", want: true},
		"white background":        {colorFGBG: "0;15", want: false},
		"256 color dark":          {colorFGBG: "15;16", want: true},
		"256 color light":         {colorFGBG: "0;231", want: false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, staticBackgroundIsDark(test.colorFGBG))
		})
	}
}
