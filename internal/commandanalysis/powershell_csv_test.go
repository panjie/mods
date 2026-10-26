package commandanalysis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestP2ImportCSVBinding(t *testing.T) {
	for _, args := range [][]string{
		{"amounts.csv"}, {"-Path", "'space name.csv'"},
		{"-LiteralPath", "'file[1].csv'", "-Encoding", "utf8"},
		{"-Delimiter", "';'", "-Path", "amounts.csv"},
		{"-Encoding", "Unicode", "amounts.csv"},
	} {
		f := catalogTokens(append([]string{"Import-Csv"}, args...), powershellSpace, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectRead, f.Effect, args)
		require.Equal(t, EvidenceStatic, f.Evidence, args)
		require.Len(t, f.Targets, 1)
		require.Equal(t, TargetRead, f.Targets[0].Role)
		require.True(t, f.Targets[0].Literal)
		require.False(t, f.KnownWrite)
	}
	for _, args := range [][]string{
		nil, {"-Path"}, {"-LiteralPath"}, {"a.csv", "b.csv"},
		{"-Path", "a.csv", "-LiteralPath", "b.csv"}, {"-Path", "a.csv", "-Path", "b.csv"},
		{"a.csv", "-Unknown"}, {"a.csv", "-OutVariable", "x"},
		{"-P", "a.csv"}, {"-Path:a.csv"}, {"-UseCulture", "a.csv"},
		{"@params"}, {"$path"}, {"$_.FullName"}, {"(Get-Item a.csv)"}, {"$(Remove-Item x)"},
		{"a.csv,b.csv"}, {"'*.csv'"}, {"'HKCU:\\Software\\X'"},
		{"a.csv", "-Encoding", "$(mystery)"}, {"a.csv", "-Encoding", "mystery"},
		{"a.csv", "-Delimiter", "'ab'"}, {"a.csv", "-Delimiter", "'😀'"},
		{"a.csv", "-Encoding", "utf8", "-Encoding", "ascii"},
	} {
		f := catalogTokens(append([]string{"Import-Csv"}, args...), powershellSpace, ReadOnlyCommandPolicy{})
		require.False(t, f.BuiltinRead, args)
		require.Equal(t, CoverageIncomplete, f.Coverage, args)
		require.Empty(t, f.Targets, args)
	}
	for _, name := range []string{"Import-Csv.exe", "Import-Csv.cmd", ".\\Import-Csv", "Module\\Import-Csv"} {
		require.False(t, catalogTokens([]string{name, "a.csv"}, powershellSpace, ReadOnlyCommandPolicy{}).BuiltinRead, name)
	}
	require.False(t, catalogProcessTokens([]string{"Import-Csv", "a.csv"}, ReadOnlyCommandPolicy{}).BuiltinRead)
}

func TestP2ForEachParameterBoundary(t *testing.T) {
	for _, args := range [][]string{{"{ Get-Content $_.FullName }"}, {"-Process", "{ Get-Content $_.FullName }"}} {
		require.True(t, catalogTokens(append([]string{"ForEach-Object"}, args...), powershellSpace, ReadOnlyCommandPolicy{}).BuiltinRead)
	}
	for _, args := range [][]string{
		{"-Parallel", "{ Get-Content $_.FullName }"}, {"-Mystery", "{ Get-Content $_.FullName }"},
		{"-Begin", "{ Get-Content $_.FullName }"}, {"-End", "{ Get-Content $_.FullName }"},
		{"-Process", "{ Get-Content $_.FullName }", "-AsJob"}, {"{ Get-Date }", "{ Get-Date }"},
		{"-MemberName", "Delete"}, {"Delete"}, nil,
	} {
		require.False(t, catalogTokens(append([]string{"ForEach-Object"}, args...), powershellSpace, ReadOnlyCommandPolicy{}).BuiltinRead, args)
	}
}
