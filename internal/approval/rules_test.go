package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuleSetScopeAndDedupe(t *testing.T) {
	scope := WorkingDirScope("/cwd")
	scoped := func(rule Rule) Rule {
		rule.ScopeKind, rule.ScopeValue = scope.Kind, scope.Value
		return rule
	}
	cwdRule := scoped(Rule{Type: DirAllow, Paths: []string{"a", "b"}})
	otherWorkingDirRule := cwdRule
	otherWorkingDirRule.ScopeValue = "/other"
	require.ElementsMatch(t, []Rule{cwdRule, otherWorkingDirRule}, Dedupe([]Rule{
		cwdRule,
		cwdRule,
		otherWorkingDirRule,
	}))

	var rules RuleSet
	rules.Add(cwdRule, cwdRule, otherWorkingDirRule)
	require.ElementsMatch(t, []Rule{cwdRule, otherWorkingDirRule}, rules.Snapshot())
	// Storage must retain legacy rows without reintroducing their old policy.
	legacy := Rule{Type: EditAll, Tool: "file_edit"}
	rules.Replace([]Rule{legacy, legacy})
	require.Equal(t, []Rule{legacy}, rules.Snapshot())
	rules.Add(cwdRule)
	require.ElementsMatch(t, []Rule{legacy, cwdRule}, rules.Snapshot())
}
