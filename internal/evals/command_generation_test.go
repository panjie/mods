package evals

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerationPhysicalLineOracle(t *testing.T) {
	files, lines := 0, 0
	for name, content := range CommandGenerationFiles() {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		files++
		scanner := bufio.NewScanner(strings.NewReader(content))
		for scanner.Scan() {
			lines++
		}
		require.NoError(t, scanner.Err())
	}
	require.Equal(t, 3, files)
	require.Equal(t, 6, lines, "blank lines and the unterminated final line count")
}

func TestCommandGenerationCaseContracts(t *testing.T) {
	cases, err := CommandGenerationCases()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(cases), 12)
	seen := map[string]bool{}
	for _, c := range cases {
		require.False(t, seen[c.ID])
		seen[c.ID] = true
		require.NotEmpty(t, c.Prompt)
		require.NotEmpty(t, c.Check)
		require.NotEmpty(t, c.PowerShell)
		require.NotEmpty(t, c.POSIX)
		require.Contains(t, []string{"allow", "ask"}, c.Decision)
	}
	require.Contains(t, OriginalCodeStatisticsCommand(), "-ErrorAction SilentlyContinue")
	require.Contains(t, OriginalCodeStatisticsCommand(), "foreach ($f in $_.Group)")
	files := CommandGenerationFiles()
	files["a.go"] = "changed"
	require.NotEqual(t, files, CommandGenerationFiles())
}

func TestGenerationAnswerRequiresIndependentExpectedValues(t *testing.T) {
	require.NoError(t, CheckGenerationAnswer(`{"files":3,"lines":6}`, map[string]float64{"files": 3, "lines": 6}))
	require.Error(t, CheckGenerationAnswer(`{"files":3,"lines":5}`, map[string]float64{"lines": 6}))
	require.Error(t, CheckGenerationAnswer(`{"lines":"6"}`, map[string]float64{"lines": 6}))
	require.Error(t, CheckGenerationAnswer(`{"lines":6}`, nil))
}

func TestGenerationMetricsDoNotRewardUnobservedOrIncompleteWork(t *testing.T) {
	yes, no, zero := true, false, 0
	require.Nil(t, (GenerationOutcome{Query: true}).ZeroReviewCompletion())
	base := GenerationOutcome{Query: true, Completed: true, Correct: &yes, ActualReviewPrompts: &zero}
	require.True(t, *base.ZeroReviewCompletion())
	for _, change := range []func(*GenerationOutcome){
		func(o *GenerationOutcome) { o.Completed = false },
		func(o *GenerationOutcome) { o.Correct = &no },
		func(o *GenerationOutcome) { o.ApprovalUnavailable = 1 },
		func(o *GenerationOutcome) { o.UnexpectedWrites = 1 },
		func(o *GenerationOutcome) { o.Query = false },
		func(o *GenerationOutcome) { one := 1; o.ActualReviewPrompts = &one },
	} {
		got := base
		change(&got)
		require.False(t, *got.ZeroReviewCompletion())
	}
}
