package approval

import (
	"net"
	"net/url"
	"sort"
	"strings"
)

// NormalizeRemoteOrigin returns the credential- and path-free origin used by
// remote write rules. Hierarchical non-file URI schemes are accepted so the
// same model works for HTTP APIs, SSH/Git, and services such as s3://bucket.
func NormalizeRemoteOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if scp, ok := normalizeSCPLikeOrigin(raw); ok {
		return scp, true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || strings.EqualFold(u.Scheme, "file") {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	hostname := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !validRemoteHostname(hostname) {
		return "", false
	}
	port := u.Port()
	if isDefaultRemotePort(scheme, port) {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, true
}

func NormalizeRemoteOrigins(values []string) []string {
	seen := map[string]struct{}{}
	var result []string
	for _, value := range values {
		origin, ok := NormalizeRemoteOrigin(value)
		if !ok {
			continue
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		result = append(result, origin)
	}
	sort.Strings(result)
	return result
}

func normalizeSCPLikeOrigin(raw string) (string, bool) {
	if strings.Contains(raw, "://") || strings.ContainsAny(raw, " \t\r\n") {
		return "", false
	}
	colon := scpDelimiter(raw)
	if colon <= 0 || colon == len(raw)-1 {
		return "", false
	}
	left := raw[:colon]
	// Drive-letter paths are local, not SCP destinations.
	if len(left) == 1 && ((left[0] >= 'A' && left[0] <= 'Z') || (left[0] >= 'a' && left[0] <= 'z')) {
		return "", false
	}
	if at := strings.LastIndexByte(left, '@'); at >= 0 {
		left = left[at+1:]
	}
	host := strings.ToLower(strings.TrimSuffix(strings.Trim(left, "[]"), "."))
	if !validRemoteHostname(host) {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "ssh://" + host, true
}

func validRemoteHostname(host string) bool {
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, r := range host {
		if r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func scpDelimiter(raw string) int {
	if open := strings.IndexByte(raw, '['); open >= 0 {
		if close := strings.Index(raw[open:], "]:"); close >= 0 {
			return open + close + 1
		}
	}
	return strings.IndexByte(raw, ':')
}

func isDefaultRemotePort(scheme, port string) bool {
	switch scheme {
	case "http":
		return port == "80"
	case "https":
		return port == "443"
	case "ssh", "git+ssh":
		return port == "22"
	case "ftp":
		return port == "21"
	case "ftps":
		return port == "990"
	default:
		return false
	}
}
