package pathutil

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type Flavor string

const (
	FlavorPOSIX      Flavor = "posix"
	FlavorPowerShell Flavor = "powershell"
	FlavorCMD        Flavor = "cmd"
)

type Options struct {
	Workspace string
	Home      string
	Env       map[string]string
	Flavor    Flavor
}

type LocationKind int

const (
	LocationUnknown LocationKind = iota
	LocationWorkspace
	LocationSafe
	LocationExternal
)

func DefaultOptions(workspace string, flavor Flavor) Options {
	home := ""
	if dir, err := os.UserHomeDir(); err == nil {
		home = dir
	}
	return Options{
		Workspace: workspace,
		Home:      home,
		Env:       envMap(),
		Flavor:    flavor,
	}
}

func ExpandToken(token string, opts Options) string {
	token = strings.TrimSpace(token)
	if token == "" || literalWorkspaceTilde(token) {
		return token
	}
	home := userHome(opts)
	if home != "" {
		if token == "~" {
			return cleanPath(home)
		}
		if strings.HasPrefix(token, "~/") || strings.HasPrefix(token, `~\`) {
			return cleanPath(joinHome(home, token[2:]))
		}
	}
	if isUnresolvedHomePath(token) {
		return cleanPath(token)
	}
	if expanded, ok := expandHomeVariable(token, opts); ok {
		return cleanPath(expanded)
	}
	return cleanPath(token)
}

func NormalizePath(token string, opts Options) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	expanded := ExpandToken(token, opts)
	if expanded == "" {
		return ""
	}
	if IsUnresolvedHomePath(expanded) {
		return cleanPath(expanded)
	}
	if IsAbs(expanded) {
		return cleanPath(expanded)
	}
	if opts.Workspace == "" {
		return cleanPath(expanded)
	}
	return joinPath(opts.Workspace, expanded)
}

func NormalizeShellPath(token string, opts Options) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if hasUnescapedShellGlob(token, opts.Flavor) {
		token = shellGlobBaseDir(token, opts.Flavor)
	}
	return NormalizePath(token, opts)
}

func NormalizeShellDirs(paths []string, opts Options) []string {
	if len(paths) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(paths))
	for _, p := range paths {
		normalized = append(normalized, NormalizeShellPath(p, opts))
	}
	return NormalizeDirs(normalized, Options{Flavor: opts.Flavor})
}

func NormalizeDirs(paths []string, opts Options) []string {
	if len(paths) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(paths))
	resolved := make([]string, 0, len(paths))
	for _, p := range paths {
		p = NormalizePath(p, opts)
		if p == "" || p == "." {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			p = ParentDir(p)
		}
		key := comparePath(p)
		if !seen[key] {
			seen[key] = true
			resolved = append(resolved, p)
		}
	}
	kept := make([]string, 0, len(resolved))
	for _, d := range resolved {
		covered := false
		for _, other := range resolved {
			if comparePath(d) == comparePath(other) {
				continue
			}
			if Contains(other, d) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, d)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return comparePath(kept[i]) < comparePath(kept[j])
	})
	return kept
}

func ParentDir(p string) string {
	p = cleanPath(p)
	if p == "" {
		return "."
	}
	if windowsStylePath(p) {
		if windowsDriveRoot(p) {
			return p
		}
		if i := strings.LastIndex(p, `\`); i >= 0 {
			if i == 0 {
				return p[:1]
			}
			if i == 2 && len(p) >= 2 && p[1] == ':' {
				return p[:i+1]
			}
			return strings.TrimRight(p[:i], `\`)
		}
		return "."
	}
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		if i == 0 {
			return p[:1]
		}
		if i == 2 && len(p) >= 2 && p[1] == ':' {
			return p[:i]
		}
		return strings.TrimRight(p[:i], `/\`)
	}
	return "."
}

func Contains(root, target string) bool {
	root = cleanPath(strings.TrimSpace(root))
	target = cleanPath(strings.TrimSpace(target))
	if root == "" || target == "" {
		return false
	}
	if root == "." {
		return target == "." || !IsAbs(target) && !IsUnresolvedHomePath(target)
	}
	r := comparePath(root)
	t := comparePath(target)
	if r == "" || t == "" {
		return false
	}
	if t == r {
		return true
	}
	return strings.HasPrefix(t, descendantPrefix(r))
}

func Location(target, workspace string, safeDirs []string) LocationKind {
	target = strings.TrimSpace(target)
	if target == "" {
		return LocationUnknown
	}
	if IsUnresolvedHomePath(target) {
		return LocationExternal
	}
	if !IsAbs(target) {
		return LocationWorkspace
	}
	target = cleanPath(target)
	if Contains(workspace, target) {
		return LocationWorkspace
	}
	for _, safe := range safeDirs {
		if Contains(safe, target) {
			return LocationSafe
		}
	}
	// Lexical comparison missed. Resolve symlinks on both the target and
	// the boundaries before concluding the path is external: a workspace
	// (or safe directory) reached through a symlink alias still counts as
	// inside the boundary. Any resolution failure falls back to the
	// lexical verdict, which fails closed (external -> review).
	resolved, err := ResolveThroughExistingParent(target)
	if err == nil {
		if ws := strings.TrimSpace(workspace); ws != "" && IsAbs(ws) {
			if wsResolved, wsErr := ResolveThroughExistingParent(cleanPath(ws)); wsErr == nil && Contains(wsResolved, resolved) {
				return LocationWorkspace
			}
		}
		for _, safe := range safeDirs {
			safe = strings.TrimSpace(safe)
			if safe == "" || !IsAbs(safe) {
				continue
			}
			if safeResolved, safeErr := ResolveThroughExistingParent(cleanPath(safe)); safeErr == nil && Contains(safeResolved, resolved) {
				return LocationSafe
			}
		}
	}
	return LocationExternal
}

func IsAbs(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	if filepath.IsAbs(p) {
		return true
	}
	if windowsDriveAbs(p) || windowsUNC(p) {
		return true
	}
	return false
}

func IsUnresolvedHomePath(p string) bool {
	return isUnresolvedHomePath(strings.TrimSpace(p))
}

func hasUnescapedShellGlob(s string, flavor Flavor) bool {
	escaped := false
	posixEscapes := flavor == FlavorPOSIX && !windowsDriveAbs(s) && !windowsUNC(s)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && posixEscapes {
			escaped = true
			continue
		}
		switch ch {
		case '*', '?', '[':
			return true
		}
	}
	return false
}

func shellGlobBaseDir(s string, flavor Flavor) string {
	first := firstUnescapedShellGlob(s, flavor)
	if first < 0 {
		return s
	}
	sep := lastSeparatorBefore(s, first)
	switch {
	case sep < 0:
		return "."
	case sep == 0 && (s[0] == '/' || s[0] == '\\'):
		return s[:1]
	case sep == 2 && len(s) >= 2 && s[1] == ':':
		return s[:sep+1]
	default:
		return strings.TrimRight(s[:sep], `/\`)
	}
}

func firstUnescapedShellGlob(s string, flavor Flavor) int {
	escaped := false
	posixEscapes := flavor == FlavorPOSIX && !windowsDriveAbs(s) && !windowsUNC(s)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && posixEscapes {
			escaped = true
			continue
		}
		switch ch {
		case '*', '?', '[':
			return i
		}
	}
	return -1
}

func lastSeparatorBefore(s string, end int) int {
	last := -1
	for i := 0; i < end; i++ {
		if s[i] == '/' || s[i] == '\\' {
			last = i
		}
	}
	return last
}

func envMap() map[string]string {
	env := make(map[string]string)
	for _, item := range os.Environ() {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			env[strings.ToUpper(k)] = v
		}
	}
	return env
}

func userHome(opts Options) string {
	if opts.Home != "" {
		return opts.Home
	}
	for _, key := range []string{"HOME", "USERPROFILE"} {
		if v := envValue(opts, key); v != "" {
			return v
		}
	}
	drive := envValue(opts, "HOMEDRIVE")
	path := envValue(opts, "HOMEPATH")
	if drive != "" && path != "" {
		return drive + path
	}
	return ""
}

func envValue(opts Options, key string) string {
	key = strings.ToUpper(key)
	if opts.Env != nil {
		for k, v := range opts.Env {
			if strings.EqualFold(k, key) {
				return v
			}
		}
		return ""
	}
	return os.Getenv(key)
}

// stableEnvPathVars lists machine-level Windows location variables whose
// values are fixed for the lifetime of the mods process. Secret environment
// injection reserves these names so a secret can never shadow a variable the
// child shell would otherwise inherit unchanged. Static path expansion
// itself covers every variable whose value is a single absolute directory
// (see EnvDirValue); callers guard non-reserved names against per-call
// secret shadowing before expanding.
var stableEnvPathVars = map[string]bool{
	"APPDATA": true, "COMSPEC": true, "LOCALAPPDATA": true, "PROGRAMDATA": true,
	"PROGRAMFILES": true, "PROGRAMFILES(X86)": true, "SYSTEMDRIVE": true,
	"SYSTEMROOT": true, "TEMP": true, "TMP": true, "WINDIR": true,
}

// IsStableEnvName reports whether name is one of the machine-level location
// variables secret environment injection reserves. Classification and the
// child shell cannot disagree about these names.
func IsStableEnvName(name string) bool {
	return stableEnvPathVars[strings.ToUpper(strings.TrimSpace(name))]
}

// publicEnvPathVars lists environment variables whose values are public
// machine metadata rather than capabilities: any local shell can already
// observe them, so a read of their content needs no review. Deliberately
// excludes anything that may carry user data, credentials, or pointers to
// private files.
var publicEnvPathVars = map[string]bool{
	"NUMBER_OF_PROCESSORS":   true,
	"OS":                     true,
	"PATH":                   true,
	"PATHEXT":                true,
	"PROCESSOR_ARCHITECTURE": true,
	"PROCESSOR_IDENTIFIER":   true,
	"PROCESSOR_LEVEL":        true,
	"PROCESSOR_REVISION":     true,
}

// IsPublicEnvName reports whether an environment variable's content is
// public machine metadata whose read requires no review. Windows (the
// PowerShell flavor) compares case-insensitively; POSIX variables are
// case-sensitive and only PATH qualifies.
func IsPublicEnvName(name string, flavor Flavor) bool {
	if flavor == FlavorPOSIX {
		return name == "PATH"
	}
	return publicEnvPathVars[strings.ToUpper(strings.TrimSpace(name))]
}

// EnvDirValue resolves an environment variable name to its value when the
// value can serve as a concrete directory for static path expansion:
// non-empty, absolute, single-line, and free of path-list separators (a
// value such as PATH names several locations and must never expand).
func EnvDirValue(name string, opts Options) (string, bool) {
	if name == "" {
		return "", false
	}
	value := envValue(opts, name)
	if !envValueDirLike(value, opts.Flavor) {
		return "", false
	}
	return value, true
}

func envValueDirLike(value string, flavor Flavor) bool {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	if !IsAbs(value) {
		return false
	}
	if flavor == FlavorPOSIX {
		return !strings.ContainsAny(value, ":;")
	}
	return !strings.Contains(value, ";")
}

// ExpandEnvPath resolves a shell environment variable reference with a
// path-shaped tail into a concrete absolute path. The variable's value must
// pass EnvDirValue hygiene so classification and the child shell agree on
// the expansion whenever the value is inherited unchanged; callers must
// additionally ensure the child observes the same value (no in-command
// environment mutation, no secret injection under the name). PowerShell
// accepts $env:NAME and ${env:NAME}; POSIX accepts $NAME and ${NAME}.
// Bare value references do not expand here; use EnvDirValue for those.
func ExpandEnvPath(token string, opts Options) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	switch opts.Flavor {
	case FlavorPowerShell:
		for _, form := range [...]string{"${env:", "$env:"} {
			if !hasCaseInsensitivePrefix(token, form) {
				continue
			}
			rest := token[len(form):]
			name, tail, ok := envPathNameAndTail(rest, strings.HasPrefix(form, "${"))
			if !ok {
				continue
			}
			value, valid := EnvDirValue(name, opts)
			if !valid {
				continue
			}
			return joinHome(value, tail), true
		}
	case FlavorPOSIX:
		rest, isRef := strings.CutPrefix(token, "$")
		if !isRef {
			return "", false
		}
		var name, tail string
		if braced, found := strings.CutPrefix(rest, "{"); found {
			end := strings.IndexByte(braced, '}')
			if end <= 0 || !validPOSIXEnvName(braced[:end]) {
				return "", false
			}
			name, tail = braced[:end], braced[end+1:]
		} else {
			n := posixEnvNameLen(rest)
			if n == 0 {
				return "", false
			}
			name, tail = rest[:n], rest[n:]
		}
		if !strings.HasPrefix(tail, "/") {
			return "", false
		}
		value, valid := EnvDirValue(name, opts)
		if !valid {
			return "", false
		}
		return joinHome(value, tail), true
	}
	return "", false
}

func envPathNameAndTail(rest string, braced bool) (name, tail string, ok bool) {
	if braced {
		end := strings.IndexByte(rest, '}')
		if end <= 0 {
			return "", "", false
		}
		name, tail = rest[:end], rest[end+1:]
	} else {
		sep := strings.IndexAny(rest, `/\`)
		if sep <= 0 {
			return "", "", false
		}
		name, tail = rest[:sep], rest[sep:]
	}
	if tail == "" || tail[0] != '/' && tail[0] != '\\' {
		return "", "", false
	}
	if !validPowerShellEnvName(name) {
		return "", "", false
	}
	return name, tail, true
}

// EnvRefParts decomposes an environment variable reference. For a
// path-shaped reference ($env:NAME\tail, ${env:NAME}\tail, $NAME/tail,
// ${NAME}/tail) it returns the variable name and pathShaped=true; for a
// bare reference it returns the name and pathShaped=false. ok is false
// when ref is not an environment reference of the requested flavor.
func EnvRefParts(ref string, flavor Flavor) (name string, pathShaped bool, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", false, false
	}
	if flavor == FlavorPowerShell {
		if hasCaseInsensitivePrefix(ref, "${env:") {
			rest := ref[len("${env:"):]
			end := strings.IndexByte(rest, '}')
			if end <= 0 || !validPowerShellEnvName(rest[:end]) {
				return "", false, false
			}
			tail := rest[end+1:]
			return rest[:end], tail != "" && isPathSeparatorByte(tail[0]), true
		}
		if hasCaseInsensitivePrefix(ref, "$env:") {
			rest := ref[len("$env:"):]
			sep := strings.IndexAny(rest, `/\`)
			if sep < 0 {
				if !validPowerShellEnvName(rest) {
					return "", false, false
				}
				return rest, false, true
			}
			if sep == 0 || !validPowerShellEnvName(rest[:sep]) {
				return "", false, false
			}
			return rest[:sep], true, true
		}
		return "", false, false
	}
	if flavor != FlavorPOSIX {
		return "", false, false
	}
	rest, isRef := strings.CutPrefix(ref, "$")
	if !isRef {
		return "", false, false
	}
	if braced, found := strings.CutPrefix(rest, "{"); found {
		end := strings.IndexByte(braced, '}')
		if end <= 0 || !validPOSIXEnvName(braced[:end]) {
			return "", false, false
		}
		tail := braced[end+1:]
		return braced[:end], tail != "" && tail[0] == '/', true
	}
	n := posixEnvNameLen(rest)
	if n == 0 {
		return "", false, false
	}
	return rest[:n], strings.HasPrefix(rest[n:], "/"), true
}

// validPowerShellEnvName reports whether name is a plausible variable name
// in PowerShell's Env: drive (letters, digits, underscore, dot, hyphen,
// parentheses).
func validPowerShellEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '_' || ch == '.' || ch == '(' || ch == ')' || ch == '-':
		default:
			return false
		}
	}
	return true
}

func validPOSIXEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch == '_':
		case ch >= '0' && ch <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// posixEnvNameLen returns the length of the leading POSIX parameter name in
// s, or 0 when s does not start with one.
func posixEnvNameLen(s string) int {
	n := 0
	for n < len(s) {
		ch := s[n]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch == '_':
			n++
		case ch >= '0' && ch <= '9':
			if n == 0 {
				return 0
			}
			n++
		default:
			return n
		}
	}
	return n
}

// EnvRefUses reports how a command text uses one environment variable.
type EnvRefUses struct {
	// Bare counts references consumed as values rather than path prefixes.
	Bare int
	// Paths lists the concrete expansions of every path-shaped reference.
	Paths []string
}

// ExpandEnvRefs scans command for references to the named environment
// variable and expands every path-shaped reference with ExpandEnvPath. It
// reports ok=false when a use cannot be resolved soundly — the variable has
// no dir-like value, or a reference is compound (adjacent to another
// expansion, escape, or concatenation) — and callers keep such references
// dynamic. Single-quoted spans never interpolate and are skipped in both
// flavors.
func ExpandEnvRefs(command, name string, opts Options) (EnvRefUses, bool) {
	if command == "" || name == "" {
		return EnvRefUses{}, false
	}
	ps := opts.Flavor == FlavorPowerShell
	if !ps && opts.Flavor != FlavorPOSIX {
		return EnvRefUses{}, false
	}
	if _, valid := EnvDirValue(name, opts); !valid {
		return EnvRefUses{}, false
	}
	var forms []string
	if ps {
		forms = []string{"$env:" + name, "${env:" + name + "}"}
	} else {
		forms = []string{"$" + name, "${" + name + "}"}
	}
	var uses EnvRefUses
	inSingle := false
	escaped := false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if inSingle {
			switch {
			case ch == '\'' && ps && i+1 < len(command) && command[i+1] == '\'':
				i++ // doubled quote stays inside a PowerShell literal
			case ch == '\'':
				inSingle = false
			}
			continue
		}
		switch {
		case ch == '\'':
			inSingle = true
		case ps && ch == '`', !ps && ch == '\\':
			escaped = true
		case ch == '$':
			next, compound := scanEnvRefAt(command, i, forms, ps, opts, &uses)
			if compound {
				return EnvRefUses{}, false
			}
			i = next
		}
	}
	return uses, true
}

// scanEnvRefAt examines the '$' at command[i] against the variable's
// reference forms. It returns the index of the last consumed byte (i when
// nothing matches) and compound=true when the reference continues into
// another expansion, escape, or concatenation that static expansion cannot
// bound.
func scanEnvRefAt(command string, i int, forms []string, ps bool, opts Options, uses *EnvRefUses) (int, bool) {
	for _, form := range forms {
		end := i + len(form)
		if end > len(command) {
			continue
		}
		if ps {
			if !strings.EqualFold(command[i:end], form) {
				continue
			}
		} else if command[i:end] != form {
			continue
		}
		braced := form[1] == '{'
		next := byte(0)
		if end < len(command) {
			next = command[end]
		}
		if !braced && isEnvNameWordChar(next, ps) {
			continue // a longer variable name, not this variable
		}
		if braced && isEnvNameWordChar(next, ps) {
			return 0, true // concatenation without a separator
		}
		if next == '/' || ps && next == '\\' {
			j := end
			for j < len(command) && isEnvPathTailChar(command[j], ps) {
				j++
			}
			if j < len(command) && refTailContinuesCompound(command, j, ps) {
				return 0, true
			}
			resolved, ok := ExpandEnvPath(command[i:j], opts)
			if !ok {
				return 0, true
			}
			uses.Paths = appendUniqueDir(uses.Paths, resolved)
			return j - 1, false
		}
		if refByteContinuesCompound(next, ps) {
			return 0, true
		}
		uses.Bare++
		return end - 1, false
	}
	return i, false
}

func refByteContinuesCompound(c byte, ps bool) bool {
	switch c {
	case '$', '\'', '"', '`':
		return true
	}
	return !ps && c == '\\'
}

// refTailContinuesCompound reports whether the byte ending a consumed path
// tail continues the word beyond static expansion: another expansion, an
// escape, or a quote that concatenates more path text (a quote followed by
// whitespace or a shell separator merely closes the word).
func refTailContinuesCompound(command string, j int, ps bool) bool {
	switch c := command[j]; c {
	case '$', '`':
		return true
	case '"', '\'':
		k := j
		for k < len(command) && (command[k] == '"' || command[k] == '\'') {
			k++
		}
		if k >= len(command) {
			return false
		}
		return isEnvPathTailChar(command[k], ps) || isEnvNameWordChar(command[k], ps)
	}
	return !ps && command[j] == '\\'
}

func isEnvNameWordChar(c byte, ps bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		return true
	}
	return ps && (c == '.' || c == '(' || c == ')')
}

// isEnvPathTailChar reports whether c may continue a path-shaped reference
// tail. Whitespace, quotes, shell separators, grouping braces, and
// expansion starters end the tail; everything else (including glob
// characters, handled later by glob truncation) continues it.
func isEnvPathTailChar(c byte, ps bool) bool {
	switch c {
	case ' ', '\t', '\r', '\n', '"', '\'', '<', '>', '|', ';', ',', '&', '(', ')', '{', '}', '$', '`':
		return false
	}
	if !ps && c == '\\' {
		return false
	}
	return c >= 0x20
}

func isPathSeparatorByte(c byte) bool {
	return c == '/' || c == '\\'
}

func appendUniqueDir(dirs []string, dir string) []string {
	for _, existing := range dirs {
		if existing == dir {
			return dirs
		}
	}
	return append(dirs, dir)
}

func expandHomeVariable(token string, opts Options) (string, bool) {
	home := userHome(opts)
	if home == "" {
		return "", false
	}
	for _, prefix := range []string{"${env:USERPROFILE}", "$env:USERPROFILE", "${HOME}", "$HOME"} {
		if hasCaseInsensitivePrefix(token, prefix) && boundaryAfterPrefix(token, len(prefix)) {
			return joinHome(home, token[len(prefix):]), true
		}
	}
	if hasCaseInsensitivePrefix(token, "%USERPROFILE%") && boundaryAfterPrefix(token, len("%USERPROFILE%")) {
		return joinHome(home, token[len("%USERPROFILE%"):]), true
	}
	driveRef := "%HOMEDRIVE%%HOMEPATH%"
	if hasCaseInsensitivePrefix(token, driveRef) && boundaryAfterPrefix(token, len(driveRef)) {
		driveHome := envValue(opts, "HOMEDRIVE") + envValue(opts, "HOMEPATH")
		if driveHome == "" {
			driveHome = home
		}
		return joinHome(driveHome, token[len(driveRef):]), true
	}
	return "", false
}

func hasCaseInsensitivePrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func boundaryAfterPrefix(s string, n int) bool {
	return len(s) == n || s[n] == '/' || s[n] == '\\'
}

func joinHome(home, rest string) string {
	rest = strings.TrimLeft(rest, `/\`)
	if rest == "" {
		return home
	}
	if windowsStylePath(home) {
		return cleanPath(home + `\` + rest)
	}
	return cleanPath(path.Join(home, strings.ReplaceAll(rest, `\`, "/")))
}

func joinPath(base, elem string) string {
	if base == "" {
		return cleanPath(elem)
	}
	if windowsStylePath(base) {
		return cleanPath(strings.TrimRight(base, `/\`) + `\` + strings.TrimLeft(elem, `/\`))
	}
	return cleanPath(path.Join(base, strings.ReplaceAll(elem, `\`, "/")))
}

func literalWorkspaceTilde(token string) bool {
	return strings.HasPrefix(token, "./~/") ||
		strings.HasPrefix(token, `.\~\`) ||
		strings.HasPrefix(token, `./~\`) ||
		strings.HasPrefix(token, `.\/~/`)
}

func isUnresolvedHomePath(p string) bool {
	if !strings.HasPrefix(p, "~") {
		return false
	}
	return p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`)
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if windowsStylePath(p) {
		return cleanWindowsPath(p)
	}
	return path.Clean(p)
}

func windowsStylePath(p string) bool {
	return windowsDriveAbs(p) || windowsDriveRel(p) || windowsUNC(p) || strings.Contains(p, `\`)
}

func windowsDriveAbs(p string) bool {
	return len(p) >= 3 && isDriveLetter(p[0]) && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

func windowsDriveRel(p string) bool {
	return len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':'
}

func windowsUNC(p string) bool {
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, `//`)
}

func windowsDriveRoot(p string) bool {
	return len(p) == 3 && isDriveLetter(p[0]) && p[1] == ':' && p[2] == '\\'
}

func isDriveLetter(ch byte) bool {
	return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func cleanWindowsPath(p string) string {
	p = strings.ReplaceAll(p, `/`, `\`)
	if strings.HasPrefix(p, `\\`) {
		rest := strings.TrimLeft(p, `\`)
		cleaned := path.Clean("/" + strings.ReplaceAll(rest, `\`, "/"))
		cleaned = strings.TrimPrefix(cleaned, "/")
		if cleaned == "." {
			return `\\`
		}
		return `\\` + strings.ReplaceAll(cleaned, "/", `\`)
	}
	cleaned := path.Clean(strings.ReplaceAll(p, `\`, "/"))
	cleaned = strings.ReplaceAll(cleaned, "/", `\`)
	if len(cleaned) == 2 && cleaned[1] == ':' {
		return cleaned + `\`
	}
	if windowsDriveAbs(cleaned) {
		return strings.TrimRight(cleaned, `\`)
	}
	return cleaned
}

func comparePath(p string) string {
	p = cleanPath(p)
	windows := windowsStylePath(p)
	p = strings.ReplaceAll(p, `\`, "/")
	if windows {
		p = strings.ToLower(p)
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") && !compareWindowsDriveRoot(p) {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func descendantPrefix(root string) string {
	if strings.HasSuffix(root, "/") {
		return root
	}
	return root + "/"
}

func compareWindowsDriveRoot(s string) bool {
	return len(s) == 3 && s[1] == ':' && s[2] == '/' && isDriveLetter(s[0])
}
