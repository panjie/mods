package commandanalysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogValidation(t *testing.T) {
	base := func() commandEntry {
		return commandEntry{ID: "native/test", Name: "test", Revision: 1, Tests: []string{"TestCatalogValidation"}, Read: readAnyArguments}
	}
	cases := map[string]func() []commandEntry{
		"duplicate ID":       func() []commandEntry { a, b := base(), base(); b.Name = "other"; return []commandEntry{a, b} },
		"duplicate identity": func() []commandEntry { a, b := base(), base(); b.ID = "other"; return []commandEntry{a, b} },
		"revision":           func() []commandEntry { a := base(); a.Revision = 0; return []commandEntry{a} },
		"missing tests":      func() []commandEntry { a := base(); a.Tests = nil; return []commandEntry{a} },
		"empty test":         func() []commandEntry { a := base(); a.Tests = []string{""}; return []commandEntry{a} },
		"unknown space":      func() []commandEntry { a := base(); a.Space = 99; return []commandEntry{a} },
		"qualified name":     func() []commandEntry { a := base(); a.Name = "/tmp/test"; return []commandEntry{a} },
		"PS case ambiguity": func() []commandEntry {
			a := base()
			a.Space = powershellSpace
			a.Name = "Test"
			return []commandEntry{a}
		},
		"PS executable identity": func() []commandEntry {
			a := base()
			a.Space = powershellSpace
			a.Name = "test.exe"
			return []commandEntry{a}
		},
		"alias missing target": func() []commandEntry { a := base(); a.Read = nil; a.AliasOf = "missing"; return []commandEntry{a} },
		"alias and rule":       func() []commandEntry { a := base(); a.AliasOf = "test"; return []commandEntry{a} },
		"alias cycle": func() []commandEntry {
			a, b := base(), base()
			a.Read = nil
			b.Read = nil
			a.AliasOf = "other"
			b.Name = "other"
			b.ID = "other"
			b.AliasOf = "test"
			return []commandEntry{a, b}
		},
		"read/write subcommand collision": func() []commandEntry {
			a := base()
			a.Write = writeGit
			a.Subcommands = []string{"show"}
			a.RepositoryWrites = []string{"show"}
			return []commandEntry{a}
		},
		"unused option grammar": func() []commandEntry {
			a := base()
			a.Options = []optionSpec{{Names: []string{"-o"}}}
			return []commandEntry{a}
		},
		"duplicate subcommand":   func() []commandEntry { a := base(); a.Subcommands = []string{"show", "show"}; return []commandEntry{a} },
		"subcommand case":        func() []commandEntry { a := base(); a.Subcommands = []string{"SHOW"}; return []commandEntry{a} },
		"bounded without writer": func() []commandEntry { a := base(); a.BoundedWrite = true; return []commandEntry{a} },
		"conflicting option": func() []commandEntry {
			a := base()
			a.Options = []optionSpec{{Names: []string{"-o"}}, {Names: []string{"-o"}, Value: true}}
			return []commandEntry{a}
		},
		"ambiguous option role": func() []commandEntry {
			a := base()
			a.Options = []optionSpec{{Names: []string{"-o"}, Value: true, Flag: "reverse"}}
			return []commandEntry{a}
		},
		"reserved option": func() []commandEntry {
			a := base()
			a.Options = []optionSpec{{Names: []string{"--"}}}
			return []commandEntry{a}
		},
	}
	for name, makeEntries := range cases {
		t.Run(name, func(t *testing.T) { _, err := newCatalog(makeEntries()); require.Error(t, err) })
	}
	require.NotPanics(t, func() { mustCatalog(builtinCommandEntries()) })
	// Construction validates descriptors only. Neither rule callbacks nor host
	// dependencies may run while registering entries.
	a := base()
	a.Read = func(*commandEntry, []string, ReadOnlyCommandPolicy, *catalogEvaluation) bool {
		panic("registration executed a rule")
	}
	require.NotPanics(t, func() { mustCatalog([]commandEntry{a}) })
}

func TestCatalogReferencesRealTests(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	names := map[string]bool{}
	for _, path := range files {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok {
				names[fn.Name.Name] = true
			}
		}
	}
	for _, entry := range builtinCommandEntries() {
		for _, name := range entry.Tests {
			require.True(t, names[name], "%s references absent %s", entry.ID, name)
		}
	}
}
