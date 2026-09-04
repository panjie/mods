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
		{"full path", "/bin/ls", false},
		{"full path with arg", "/usr/bin/cat file", false},
		{"workspace executable with allowed basename", "./cat README.md", false},

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
		{"param exp", "echo $VAR", true},
		{"param exp braced", "cat ${FILE}", true},
		{"home param path", `cat "$HOME/Downloads/file"`, true},
		{"braced home param path", `cat "${HOME}/Downloads/file"`, true},
		{"home param modifier", `cat "${HOME:-$(touch owned.txt)}/file"`, false},
		{"env wraps readonly command", "env LC_ALL=C git status", true},
		{"env alone with assignment", "env PATH=./bin", true},

		// --- Option-sensitive read-only commands ---
		{"find print0", `find "$HOME/Downloads" -type f -print0`, true},
		{"find exec reader", `find . -name '*.go' -exec cat {} +`, true},
		{"find exec reader pipeline", `find . -name '*.go' -exec cat {} + | wc -l`, true},
		{"sort numeric", "sort -n", true},
		{"sort combined flags", "sort -rn", true},
		{"sed print range", "sed -n '1,60p' /usr/bin/tool", true},
		{"sed substitute", "sed 's/a/b/' file", true},
		{"sed expression flag", "sed -n -e 's/a/b/' -e '$p' file", true},
		{"sed combined short flags", "sed -ne '5p' file", true},
		{"sed file operand with w", "sed -n '1,60p' /var/www/config", true},
		{"sed script from file", "sed -f script.sed file", false},
		{"sed expression script file", "sed --file=script.sed file", false},
		{"sed in-place", "sed -i 's/a/b/' file", false},
		{"sed in-place suffix", "sed -i.bak 's/a/b/' file", false},
		{"sed in-place long", "sed --in-place 's/a/b/' file", false},
		{"sed follow symlinks", "sed --follow-symlinks -n 1p file", false},
		{"sed write command", "sed 's/a/b/w owned.txt' file", false},
		{"sed write operand command", "sed 'w owned.txt' file", false},
		{"sed uppercase write command", "sed 'W owned.txt' file", false},
		{"sed write inside expression", "sed --expression='s/a/b/w owned.txt' file", false},
		{"sed script regex mentions w", "sed 's/hw/e/' file", false},
		{"sed unknown option", "sed -X '1p' file", false},
		{"sed unknown long option", "sed --unknown '1p' file", false},
		{"sed missing script", "sed", false},
		{"sed trailing expression without script", "sed -n", false},
		{"zcat kernel config", "zcat /proc/config.gz", true},
		{"zcat pipe grep", "zcat /proc/config.gz | grep -i limine | head", true},
		{"xargs stat", `xargs -0 stat -f '%m %N'`, true},
		{
			"oldest downloads pipeline",
			`find "$HOME/Downloads" -type f -print0 | xargs -0 stat -f '%m %N' | sort -n | head -1`,
			true,
		},

		// --- Multiple statements ---
		{"multi stmt", "git status; git diff", true},

		// --- NOT read-only ---
		{"output redirect", "echo hello > file.txt", false},
		{"append redirect", "ls >> out.log", false},
		{"pipe with tee", "cat file | tee output", false},
		{"rm", "rm file", false},
		{"find delete", "find . -delete", false},
		{"find exec", "find . -exec rm {} +", false},
		{"find exec shell", `find . -exec sh -c 'touch "$1"' sh {} +`, false},
		{"find unknown primary", "find . -unknown", false},
		{"sort output", "sort -o output file", false},
		{"sort temp dir", "sort -T /tmp file", false},
		{"sort unknown option", "sort --unknown file", false},
		{"xargs rm", "xargs -0 rm", false},
		{"xargs touch", "xargs touch", false},
		{"xargs unknown nested command", "xargs custom-reader", false},
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
		{"prefix assignment wraps reader", "PATH=./bin cat README.md", false},
		{"env path wraps reader", "env PATH=./bin cat README.md", false},
		{"env loader hook wraps reader", "env LD_PRELOAD=./payload cat README.md", false},
		{"git external diff environment hook", "env GIT_EXTERNAL_DIFF=./payload git diff", false},
		{"git diff output file", "git diff --output=owned.txt", false},
		{"git show output file", "git show --output owned.txt HEAD", false},
		{"git diff external helper", "git diff --ext-diff", false},
		{"go vet external tool", "go vet -vettool=./payload ./...", false},
		{"go list external tool", "go list -toolexec=./payload ./...", false},
		{"kubectl external diff hook", "kubectl diff -f deploy.yml", false},
		{"xxd reverse writes output", "xxd -r input.hex output.bin", false},
		{"empty", "", false},
		{"bare git", "git", false},
		{"bare docker", "docker", false},
		{"git global flag", "git --git-dir=/x status", false},
		{"unknown subcmd", "git push", false},
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

func TestIsReadOnlyPOSIXWithPolicy(t *testing.T) {
	policy := ReadOnlyCommandPolicy{Commands: []string{"rg", "jq", "find"}}
	readOnly := []string{
		"rg needle README.md",
		"env LC_ALL=C rg needle README.md",
		"rg needle README.md | jq .",
		"find . -delete",
	}
	for _, command := range readOnly {
		t.Run("read "+command, func(t *testing.T) {
			got, reason := IsReadOnlyPOSIXWithPolicy(command, policy)
			require.True(t, got)
			require.NotEmpty(t, reason)
		})
	}

	notReadOnly := []string{
		"/usr/bin/rg needle README.md",
		"./rg needle README.md",
		"rg needle README.md > matches.txt",
		"rg needle README.md &",
		"rg needle README.md | rm output.txt",
		"RG needle README.md",
		"$COMMAND needle README.md",
	}
	for _, command := range notReadOnly {
		t.Run("not read "+command, func(t *testing.T) {
			got, _ := IsReadOnlyPOSIXWithPolicy(command, policy)
			require.False(t, got)
		})
	}
}
