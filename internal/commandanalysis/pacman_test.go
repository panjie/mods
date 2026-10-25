package commandanalysis

import (
	"context"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestPacmanQueries(t *testing.T) {
	for _, tc := range []struct {
		args string
		read bool
	}{
		{"-Si dust duf", true}, {"-Ss -q ncdu", true}, {"-Q ncdu gdu dust duf btop", true}, {"-Qdtq", true}, {"--query --info dust", true}, {"--sync --search dust", true}, {"-Sl", true}, {"--version", true},
		{"-S dust", false}, {"-Syu", false}, {"-Siy dust", false}, {"-Sy --info dust", false}, {"--sync --search --refresh dust", false}, {"-Sw dust", false}, {"-Sc", false}, {"-R dust", false}, {"-U file", false}, {"-D --asdeps dust", false}, {"-Q --logfile /tmp/log", false}, {"-Q --config /tmp/config", false}, {"-Q --unknown", false}, {"--syn --info dust", false}, {"-Q -S dust", false}, {"-Q --sysroot /tmp/root", false}, {"-S -- --info", false},
	} {
		t.Run(tc.args, func(t *testing.T) {
			args := strings.Fields(tc.args)
			f := catalogTokens(append([]string{"pacman"}, args...), nativeSpace, ReadOnlyCommandPolicy{})
			require.Equal(t, tc.read, f.BuiltinRead)
			e := NewEngine(Settings{}, Dependencies{})
			r, err := e.Analyze(context.Background(), Input{Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "pacman", Args: args}})
			require.NoError(t, err)
			require.Equal(t, tc.read, r.Effect == EffectRead)
		})
	}
}
