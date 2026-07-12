package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReadOnlyPOSIX(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		// --- Simple allowlist commands ---
		{"simple ls", "ls", true},
		{"ls with flags", "ls -la", true},
		{"cat file", "cat README.md", true},
		{"diff two files", "diff a b", true},
		{"tr transform", "tr a-z A-Z", true},
		{"printf", `printf "hello"`, true},
		{"test builtin", "test -f x", true},
		{"bracket test", "[ -f x ]", true},
		{"true", "true", true},
		{"false", "false", true},
		{"seq", "seq 1 10", true},
		{"md5sum", "md5sum file", true},
		{"full path", "/bin/ls", true},
		{"full path with arg", "/usr/bin/cat file", true},

		// --- Pipes (all leaves read-only) ---
		{"pipe cat grep", "cat file | grep foo", true},
		{"pipe ls head", "ls -la | head -5", true},
		{"pipe echo tr", "echo hello | tr a-z A-Z", true},
		{"triple pipe", "cat file | grep foo | head -5", true},

		// --- Binary && / || (both sides read-only) ---
		{"and git", "git status && git diff", true},
		{"or true false", "true || false", true},
		{"and test echo", "test -f x && echo y", true},
		{"cd then git", "cd /repo && git describe --tags --always", true},
		{"cd then ls", "cd /repo && ls -la", true},

		// --- Shell builtins (filesystem-safe) ---
		{"cd alone", "cd /repo", true},
		{"cd home", "cd ~", true},

		// --- Subcommands ---
		{"git status", "git status", true},
		{"git log", "git log --oneline", true},
		{"git diff", "git diff", true},
		{"git show", "git show HEAD", true},
		{"git blame", "git blame file.go", true},
		{"docker ps", "docker ps -a", true},
		{"docker logs", "docker logs container", true},
		{"kubectl get", "kubectl get pods", true},
		{"kubectl describe", "kubectl describe pod x", true},
		{"go version", "go version", true},
		{"go list", "go list ./...", true},
		{"npm list", "npm list", true},
		{"pnpm ls", "pnpm ls", true},

		// --- Subshells ---
		{"subshell git", "(git status)", true},
		{"subshell multi", "(ls -la; cat file)", true},

		// --- Command substitution (inner read-only) ---
		{"cmdsubst date", "echo $(date)", true},
		{"cmdsubst git", "echo $(git status)", true},
		{"cmdsubst git ls-files", "cat $(git ls-files)", true},

		// --- Input redirect ---
		{"input redirect", "tr a-z A-Z < input", true},
		{"stderr redirect to dev null", "ls missing 2>/dev/null", true},

		// --- ParamExp ---
		{"param exp", "echo $VAR", false},
		{"param exp braced", "cat ${FILE}", false},
		{"env wraps readonly command", "env LC_ALL=C git status", true},

		// --- Multiple statements ---
		{"multi stmt", "git status; git diff", true},

		// --- NOT read-only ---
		{"output redirect", "echo hello > file.txt", false},
		{"append redirect", "ls >> out.log", false},
		{"pipe with tee", "cat file | tee output", false},
		{"rm", "rm file", false},
		{"find", "find . -name '*.go'", false},
		{"sort", "sort file", false},
		{"make", "make", false},
		{"git push", "git push", false},
		{"git commit", "git commit -m msg", false},
		{"docker run", "docker run img", false},
		{"cmdsubst with rm", "echo $(rm file)", false},
		{"procsubst", "diff <(cmd1) <(cmd2)", false},
		{"if clause", "if true; then ls; fi", false},
		{"for loop", "for f in *.go; do echo $f; done", false},
		{"background", "ls &", false},
		{"dynamic cmd name", "$CMD file", false},
		{"env wraps writer", "env touch owned.txt", false},
		{"env assignment wraps writer", "env LC_ALL=C rm -rf build", false},
		{"git diff output file", "git diff --output=owned.txt", false},
		{"git show output file", "git show --output owned.txt HEAD", false},
		{"git diff external helper", "git diff --ext-diff", false},
		{"xxd reverse writes output", "xxd -r input.hex output.bin", false},
		{"empty", "", false},
		{"bare git", "git", false},
		{"bare docker", "docker", false},
		{"git global flag", "git --git-dir=/x status", false},
		{"unknown subcmd", "git push", false},
		{"sed", "sed 's/a/b/' file", false},
		{"awk", "awk '{print $1}'", false},
		{"curl", "curl https://example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := IsReadOnlyPOSIX(c.cmd)
			require.Equalf(t, c.want, got, "cmd=%q", c.cmd)
		})
	}
}
