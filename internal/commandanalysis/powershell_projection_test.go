package commandanalysis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestP2MeasureProperties(t *testing.T) {
	for _, tc := range []struct{ args, want []string }{
		{nil, []string{"count"}},
		{[]string{"-Property", "amount", "-Sum"}, []string{"count", "sum"}},
		{[]string{"-Minimum", "-Maximum", "-Average"}, []string{"count", "minimum", "maximum", "average"}},
		{[]string{"-Line", "-Word", "-Character"}, []string{"lines", "words", "characters"}},
	} {
		f := catalogTokens(append([]string{"Measure-Object"}, tc.args...), powershellSpace, ReadOnlyCommandPolicy{})
		require.Equal(t, tc.want, f.ReadProperties)
	}
	for _, args := range [][]string{
		{"-Mystery"}, {"-InputObject", "$x"}, {"-OutVariable", "script:x"}, {"-Property"},
		{"-Property", "a,b"}, {"-Property", "*"}, {"-Property", "{ Remove-Item x }"},
		{"-Sum", "-Sum"}, {"-Line", "-Sum"}, {"-Sum", "-Line"}, {"-S"}, {"-Sum:$true"},
		{"-Property", "amount", "-Property", "other"},
	} {
		require.Empty(t, measureOutputProperties(args), args)
	}
	for _, name := range []string{"Measure-Object.exe", ".\\Measure-Object", "Custom\\Measure-Object"} {
		require.Empty(t, catalogTokens([]string{name}, powershellSpace, ReadOnlyCommandPolicy{}).ReadProperties)
	}
	require.Empty(t, catalogProcessTokens([]string{"Measure-Object"}, ReadOnlyCommandPolicy{}).ReadProperties)
	require.False(t, safeTopLevelPowerShellValues("(Measure-Object).Count", &psBridgeIR{TopLevelValueExpressions: []string{"(Measure-Object).Count"}}, newSemanticSession(nil, ReadOnlyCommandPolicy{})), "old IR cannot grant new projection evidence")
}
