package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/stretchr/testify/require"
)

func TestCommandArchitectureHasOneProductionBoundary(t *testing.T) {
	_, err := os.Stat("../approval/command_compat.go")
	require.True(t, os.IsNotExist(err), "removed facade must not return")
	for _, dir := range []string{".", "../approval"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			require.NoError(t, err)
			analysisAlias := ""
			for _, imp := range file.Imports {
				name, err := strconv.Unquote(imp.Path.Value)
				require.NoError(t, err)
				require.False(t, strings.Contains(name, "mvdan.cc/sh"), "parser belongs in commandanalysis: %s", path)
				if name == "github.com/panjie/mods/internal/commandanalysis" {
					analysisAlias = "commandanalysis"
					if imp.Name != nil {
						analysisAlias = imp.Name.Name
					}
				}
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if fn, ok := node.(*ast.FuncDecl); ok {
					require.NotEqual(t, "AccessIntent", fn.Name.Name, "no alternate command conversion: %s", path)
					if dir == "../approval" {
						require.False(t, strings.HasPrefix(fn.Name.Name, "Assess") || strings.HasPrefix(fn.Name.Name, "IsReadOnly") || strings.HasPrefix(fn.Name.Name, "AnalyzeCommand") || strings.HasPrefix(fn.Name.Name, "AnalyzeProcess"), "command semantics must stay in commandanalysis: %s", path)
					}
				}
				sel, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != analysisAlias {
					return true
				}
				name := sel.Sel.Name
				if dir == "../approval" {
					require.True(t, name == "TargetCoverage" || name == "AssessmentGap" || strings.HasPrefix(name, "Coverage") || strings.HasPrefix(name, "Gap") || name == "NormalizeRemoteOrigin" || name == "NormalizeRemoteOrigins", "approval must not analyze commands: %s %s", path, name)
				} else {
					require.False(t, strings.HasPrefix(name, "AssessShell") || strings.HasPrefix(name, "AssessArgv") || strings.HasPrefix(name, "IsReadOnly") || strings.HasPrefix(name, "AnalyzeCommand") || strings.HasPrefix(name, "AnalyzeProcess"), "production analysis must enter through Engine: %s %s", path, name)
				}
				return true
			})
		}
	}
}

func TestReadAuthorizationRequiresCompleteStaticOrTrustedEvidence(t *testing.T) {
	for _, evidence := range []commandanalysis.Evidence{commandanalysis.EvidenceStatic, commandanalysis.EvidenceTrust} {
		r := commandanalysis.Result{Effect: commandanalysis.EffectRead, Evidence: evidence, WriteCoverage: commandanalysis.CoverageComplete}
		require.Equal(t, AccessRead, commandResultToIntent(r).DominantClass())
		r.Evidence = commandanalysis.EvidenceUnknown
		require.Equal(t, AccessWrite, commandResultToIntent(r).DominantClass())
	}
}
