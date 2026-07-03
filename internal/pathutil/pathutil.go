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
	return cleanPath(filepath.Join(opts.Workspace, expanded))
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
	return LocationExternal
}

func IsAbs(p string) bool {
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
	return cleanPath(filepath.Join(home, strings.ReplaceAll(rest, `\`, string(filepath.Separator))))
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
	return filepath.Clean(p)
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
