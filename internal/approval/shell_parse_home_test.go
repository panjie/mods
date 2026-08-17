package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPOSIXHasUnquotedBareHomeArg(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "standalone argument", command: `cd ~; sed -n '98,104p' .spacemacs`, want: true},
		{name: "double quoted", command: `cd "~"`, want: false},
		{name: "single quoted", command: `cd '~'`, want: false},
		{name: "embedded suffix", command: `echo foo~`, want: false},
		{name: "home child path", command: `cat ~/.spacemacs`, want: false},
		{name: "heredoc body", command: "cat <<'EOF'\n~\nEOF", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, POSIXHasUnquotedBareHomeArg(tt.command))
		})
	}
}
