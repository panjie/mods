package approval

// Shell path facts extracted from command text.
//
// This is the conservative literal fallback that complements the AST/IR
// analyses in shell_static.go and assessment.go: those derive directories from
// a real parse and are authoritative, but their command-specific target
// extraction deliberately misses valid literal forms. Both sources are merged
// by the caller (internal/app) which must never drop an authoritative fact in
// favour of this one.

import (
	"regexp"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
	"mvdan.cc/sh/v3/syntax"
)

// Path-extraction patterns for ExternalShellPathFacts. The *Path
// variants capture the full token so it can be populated into KnownDirs.
var (
	reParentPath        = regexp.MustCompile(`\.\.[\\/][^\s'"<>|;,&(){}]*`)
	reHomePath          = regexp.MustCompile(`~[\\/a-zA-Z][^\s'"<>|;,&(){}]*`)
	reHomeVarPath       = regexp.MustCompile(`(?i)\$(?:\{(?:HOME|env:USERPROFILE)\}|env:USERPROFILE|HOME)[\\/][^\s'"<>|;,&(){}]*`)
	reCMDHomePath       = regexp.MustCompile(`(?i)%(?:USERPROFILE|HOMEDRIVE%%HOMEPATH)%[\\/][^\s'"<>|;,&(){}]*`)
	reUnixAbsPath       = regexp.MustCompile(`(?:^|[\s="'"])(/(?:[A-Za-z0-9._][^\s'"<>|;,&(){}]*)?)`)
	reSingleQuoted      = regexp.MustCompile(`'[^']*'`)
	reDoubleQuotedValue = regexp.MustCompile(`"([^"\r\n]*)"`)
	reWinAbsPath        = regexp.MustCompile(`(?:^|[\s='"])([A-Za-z]:[\\/][^\s'"<>|;,&(){}]*)`)
	reWinUNCPath        = regexp.MustCompile(`(?:^|[\s='"])(\\\\[^\\/\s'"<>|;,&(){}]+[\\/][^\s'"<>|;,&(){}]*)`)
)

// ExternalShellPathFacts returns path tokens from the command that
// reference locations outside the cwd: absolute paths not under
// cwdDir, home-expanded paths (~/ and ~user), and parent-traversal paths
// (../). The results populate KnownDirs so ClassifyAccess and risk labels can
// correctly identify external access even when the LLM omits them. The bool
// reports whether an unquoted bare ~ was resolved, which callers must not
// override with classifier-supplied guesses.
func ExternalShellPathFacts(command, cwdDir string, flavor pathutil.Flavor, policy ReadOnlyCommandPolicy) ([]string, bool) {
	originalCommand := command
	opts := pathutil.DefaultOptions(cwdDir, flavor)
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = trimTruncatedSubstitutionPath(p)
		p = pathutil.NormalizeShellPath(p, opts)
		if pathutil.Location(p, cwdDir, nil) != pathutil.LocationExternal {
			return
		}
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}

	if flavor == pathutil.FlavorPowerShell {
		// Windows/PowerShell branch: compiler flags (/out, /target,
		// /reference) share leading-slash syntax with Unix absolute paths,
		// while "/" is also PowerShell's division operator. Keep this branch
		// strictly on Windows/PowerShell path syntax so POSIX-looking tokens
		// are not misclassified as filesystem paths. Quoted strings are only
		// treated as paths when the full literal starts with explicit path syntax;
		// single-quoted strings are then stripped to avoid false positives from
		// script literals.
		addPowerShellQuotedPathArgs(command, add)
		command = stripPowerShellSingleQuotedStrings(command)
		command = reDoubleQuotedValue.ReplaceAllString(command, " ")
		for _, m := range reWinAbsPath.FindAllStringSubmatch(command, -1) {
			add(m[1])
		}
		for _, m := range reWinUNCPath.FindAllStringSubmatch(command, -1) {
			add(m[1])
		}
		for _, m := range reHomePath.FindAllString(command, -1) {
			add(m)
		}
		for _, m := range reHomeVarPath.FindAllString(command, -1) {
			add(m)
		}
		for _, m := range reCMDHomePath.FindAllString(command, -1) {
			add(m)
		}
		for _, m := range reParentPath.FindAllString(command, -1) {
			add(m)
		}
		return paths, false
	}

	// POSIX branch: Unix absolute paths (including single-segment like
	// /etc, /tmp) are valid filesystem references. Heredoc bodies are
	// blanked first so embedded path-like tokens don't produce false
	// positives, then single-quoted script literals are stripped.
	command = blankPOSIXHeredocBodies(command)
	command = reSingleQuoted.ReplaceAllString(command, " ")
	for _, m := range reUnixAbsPath.FindAllStringSubmatch(command, -1) {
		add(m[1])
	}
	for _, m := range reWinAbsPath.FindAllStringSubmatch(command, -1) {
		add(m[1])
	}
	for _, m := range reHomePath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reHomeVarPath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reCMDHomePath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reParentPath.FindAllString(command, -1) {
		add(m)
	}
	hasBareHome := POSIXHasUnquotedBareHomeArg(originalCommand)
	if hasBareHome {
		add("~")
	}
	// The raw-text scan strips single-quoted programs to avoid interpreting
	// awk/sed regex syntax as paths. Recover genuine quoted path arguments
	// from the shell AST: only values that begin with explicit external-path
	// syntax are considered, so quoted program bodies remain ignored.
	if readOnly, _ := IsReadOnlyPOSIXWithPolicy(originalCommand, policy); readOnly {
		for _, arg := range StaticPOSIXLiteralArgs(originalCommand) {
			if arg == "~" {
				// A bare tilde in the literal recovery list may be quoted in
				// the source, which the child shell never expands; the
				// unquoted form is already covered by the bare-home check.
				continue
			}
			if IsExplicitShellPathArg(arg) {
				add(arg)
			}
		}
	}
	return paths, hasBareHome
}

// trimTruncatedSubstitutionPath repairs raw-text path tokens cut off at a
// command-substitution boundary. The extraction patterns' character classes
// exclude "(", so a path such as ~/cfg/backup.$(date +%s) yields a dangling
// "~/cfg/backup.$" token; a trailing "$" can only be such an artifact (a
// complete expansion continues past it, and an unquoted "$" at word end is
// literal). Trimming back to the last path-separator boundary recovers the
// deterministic enclosing directory instead of re-dynamizing the scope.
func trimTruncatedSubstitutionPath(token string) string {
	if !strings.HasSuffix(token, "$") {
		return token
	}
	idx := strings.LastIndexAny(token, `/\`)
	if idx < 0 {
		return ""
	}
	return token[:idx+1]
}

func addPowerShellQuotedPathArgs(command string, add func(string)) {
	for _, value := range powerShellSingleQuotedValues(command) {
		if IsExplicitPowerShellPathArg(value) {
			add(value)
		}
	}
	for _, m := range reDoubleQuotedValue.FindAllStringSubmatch(command, -1) {
		if IsExplicitPowerShellPathArg(m[1]) {
			add(m[1])
		}
	}
}

func powerShellSingleQuotedValues(command string) []string {
	var values []string
	for i := 0; i < len(command); i++ {
		if command[i] != '\'' {
			continue
		}
		var value strings.Builder
		for j := i + 1; j < len(command); j++ {
			if command[j] != '\'' {
				value.WriteByte(command[j])
				continue
			}
			if j+1 < len(command) && command[j+1] == '\'' {
				value.WriteByte('\'')
				j++
				continue
			}
			values = append(values, value.String())
			i = j
			break
		}
	}
	return values
}

func stripPowerShellSingleQuotedStrings(command string) string {
	var stripped strings.Builder
	for i := 0; i < len(command); i++ {
		if command[i] != '\'' {
			stripped.WriteByte(command[i])
			continue
		}
		closing := -1
		for j := i + 1; j < len(command); j++ {
			if command[j] != '\'' {
				continue
			}
			if j+1 < len(command) && command[j+1] == '\'' {
				j++
				continue
			}
			closing = j
			break
		}
		if closing == -1 {
			stripped.WriteByte(command[i])
			continue
		}
		stripped.WriteByte(' ')
		i = closing
	}
	return stripped.String()
}

func IsExplicitShellPathArg(arg string) bool {
	if arg == "~" || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `..\`) || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
		return true
	}
	if reHomeVarPath.MatchString(arg) || reCMDHomePath.MatchString(arg) || reWinAbsPath.MatchString(" "+arg) {
		return true
	}
	return strings.HasPrefix(arg, "~") && (strings.Contains(arg, "/") || strings.Contains(arg, `\`))
}

// IsExplicitPowerShellPathArg reports whether a token is unambiguously a path
// in the PowerShell dialect. PowerShell arguments share leading-slash syntax
// with native-program flags (/out, /reference) and "/" is also its division
// operator, so a token without explicit path syntax is not treated as a
// filesystem path there.
func IsExplicitPowerShellPathArg(arg string) bool {
	arg = strings.TrimSpace(arg)
	if arg == "~" || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `..\`) || hasDotPrefixedParentTraversal(arg) || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
		return true
	}
	if len(arg) >= 3 && ((arg[0] >= 'A' && arg[0] <= 'Z') || (arg[0] >= 'a' && arg[0] <= 'z')) && arg[1] == ':' && (arg[2] == '\\' || arg[2] == '/') {
		return true
	}
	if isExplicitWindowsUNCPath(arg) {
		return true
	}
	lower := strings.ToLower(arg)
	for _, prefix := range []string{"${env:userprofile}", "$env:userprofile", "${home}", "$home", "%userprofile%", "%homedrive%%homepath%"} {
		if strings.HasPrefix(lower, prefix+`\`) || strings.HasPrefix(lower, prefix+"/") {
			return true
		}
	}
	return strings.HasPrefix(arg, "~") && (strings.Contains(arg, "/") || strings.Contains(arg, `\`))
}

func isExplicitWindowsUNCPath(arg string) bool {
	if !strings.HasPrefix(arg, `\\`) && !strings.HasPrefix(arg, `//`) {
		return false
	}
	rest := arg[2:]
	serverEnd := strings.IndexAny(rest, `/\`)
	if serverEnd <= 0 {
		return false
	}
	server := rest[:serverEnd]
	if strings.ContainsAny(server, " \t\r\n'\"<>|;,&(){}") {
		return false
	}
	shareAndRest := rest[serverEnd+1:]
	if shareAndRest == "" {
		return false
	}
	shareEnd := strings.IndexAny(shareAndRest, `/\`)
	share := shareAndRest
	if shareEnd >= 0 {
		share = shareAndRest[:shareEnd]
	}
	return share != "" && !strings.ContainsAny(share, `<>:"|?*`)
}

func hasDotPrefixedParentTraversal(arg string) bool {
	if len(arg) < len("./..") || arg[0] != '.' || !IsShellPathSeparator(arg[1]) || arg[2] != '.' || arg[3] != '.' {
		return false
	}
	return len(arg) == len("./..") || IsShellPathSeparator(arg[4])
}

func IsShellPathSeparator(ch byte) bool {
	return ch == '/' || ch == '\\'
}

func blankPOSIXHeredocBodies(command string) string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return command
	}

	buf := []byte(command)
	syntax.Walk(file, func(node syntax.Node) bool {
		redir, ok := node.(*syntax.Redirect)
		if !ok || redir.Hdoc == nil {
			return true
		}
		startPos := redir.Hdoc.Pos()
		endPos := redir.Hdoc.End()
		if !startPos.IsValid() || !endPos.IsValid() {
			return true
		}
		blankRangePreserveLines(buf, int(startPos.Offset()), int(endPos.Offset()))
		return true
	})
	return string(buf)
}

func blankRangePreserveLines(buf []byte, start, end int) {
	if start < 0 {
		start = 0
	}
	if end > len(buf) {
		end = len(buf)
	}
	if start >= end {
		return
	}
	for i := start; i < end; i++ {
		if buf[i] != '\n' && buf[i] != '\r' {
			buf[i] = ' '
		}
	}
}
