package mcpclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/panjie/mods/internal/netutil"
)

// sensitiveEnvPatterns matches environment variable NAMES that mods refuses
// to forward to third-party MCP subprocesses by default. The list errs on
// the side of caution: any variable that commonly carries an API token,
// credential, or other secret is dropped. Users who deliberately want to
// share specific secrets with a trusted server can override the filter
// per-server with `pass-env-all: true` or by re-exporting the value via
// the explicit `env:` block on the MCP server config.
//
// The patterns are anchored at name boundaries (word breaks or anchors) to
// avoid false positives on legitimate paths or build flags.
var sensitiveEnvPatterns = []*regexp.Regexp{
	// Generic secret-bearing suffixes anywhere in the name.
	regexp.MustCompile(`(?i)(API_KEY|APIKEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|CREDENTIALS|PRIVATE_KEY)`),
	// Common provider prefixes whose env vars almost always carry tokens.
	regexp.MustCompile(`(?i)^(OPENAI|ANTHROPIC|GOOGLE|GEMINI|VERTEX|COHERE|MISTRAL|GROQ|OLLAMA|PERPLEXITY|DEEPSEEK|OPENROUTER|HUGGINGFACE|HF|XAI|CLAUDE|REPLICATE|TOGETHER|FIREWORKS|MOONSHOT|ZHIPU|BAIDU|ALIYUN|DASHSCOPE)_`),
	// Cloud / CI / package-registry secrets.
	regexp.MustCompile(`(?i)^(AWS|GCP|AZURE|GITHUB|GITLAB|BITBUCKET|CIRCLECI|TRAVIS|JENKINS|DOCKER|DOCKERHUB|NPM|PYPI|CARGO|MAVEN|HEROKU|VERCEL|NETLIFY|CLOUDFLARE|FLY|RAILWAY|SENTRY|STRIPE|TWILIO|SLACK|TELEGRAM|DISCORD|JIRA|CONFLUENCE)_`),
	// Local credential agents and key material.
	regexp.MustCompile(`(?i)^(SSH|GPG|GNUPG|KUBECONFIG|KUBE)_`),
	// mods' own configuration should not leak into subprocesses; the
	// server gets only what is explicitly listed under its env: block.
	regexp.MustCompile(`(?i)^MODS_`),
}

// filterEnvForMCPSubprocess returns env with names matching any sensitive
// pattern removed. Each entry is in the standard "KEY=VALUE" form; entries
// without an '=' are dropped as malformed.
func filterEnvForMCPSubprocess(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if isSensitiveEnvName(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isSensitiveEnvName(name string) bool {
	for _, re := range sensitiveEnvPatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// mcpSubprocessEnv composes the environment to expose to a stdio MCP child
// process. Sensitive parent variables are filtered out unless the caller
// explicitly opts in. The server's own server.Env block is appended last
// and is never filtered, since those values are explicitly supplied by
// the user for that specific server.
func mcpSubprocessEnv(server MCPServerConfig) []string {
	parent := os.Environ()
	if !server.PassEnvAll {
		parent = filterEnvForMCPSubprocess(parent)
	}
	return append(parent, server.Env...)
}

// validateMCPRemoteURL rejects MCP sse/http URLs that target loopback,
// private, link-local, or otherwise internal hosts. Without this check, a
// configuration error (or a config supplied by an untrusted source) could
// silently turn the MCP transport into an SSRF probe against internal
// services or cloud metadata endpoints.
//
// The check is bypassed when MODS_MCP_ALLOW_PRIVATE=1, mirroring the
// MODS_WEB_SEARCH_ALLOW_PRIVATE escape hatch in the websearch package.
// Only http:// and https:// schemes are accepted; other schemes are not
// supported by the MCP transports and would surface as opaque errors
// later anyway.
func validateMCPRemoteURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("mcp: remote server URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("mcp: invalid URL %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("mcp: unsupported URL scheme %q (only http and https are allowed)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("mcp: URL %q is missing a host", rawURL)
	}
	if mcpPrivateAllowed() {
		return nil
	}
	if isLocalHostname(host) {
		return fmt.Errorf("mcp: host %q is a loopback alias; set MODS_MCP_ALLOW_PRIVATE=1 to allow", host)
	}
	if ip := net.ParseIP(host); ip != nil && isInternalAddress(ip) {
		return fmt.Errorf("mcp: host %q is a private/loopback/link-local address; set MODS_MCP_ALLOW_PRIVATE=1 to allow", host)
	}
	return nil
}

// mcpPrivateAllowed reports whether the user has opted in to remote MCP
// URLs that resolve to internal addresses. Read on every call so tests
// can flip the env var with t.Setenv.
func mcpPrivateAllowed() bool {
	return os.Getenv("MODS_MCP_ALLOW_PRIVATE") == "1"
}

// isLocalHostname reports whether a host string is a well-known loopback
// alias that does not resolve via net.ParseIP.
func isLocalHostname(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "localhost.localdomain", "ip6-localhost":
		return true
	}
	return false
}

// isInternalAddress reports whether an IP literal targets an internal
// destination that an untrusted MCP server URL must not reach.
func isInternalAddress(ip net.IP) bool {
	return netutil.IsBlockedAddress(ip)
}

func mcpHTTPClient() *http.Client {
	return &http.Client{
		Transport: netutil.SafeTransport(netutil.SafeTransportOptions{
			AllowPrivate: mcpPrivateAllowed,
			ErrorPrefix:  "mcp",
		}),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("mcp: stopped after 10 redirects")
			}
			return validateMCPRemoteURL(req.URL.String())
		},
	}
}
