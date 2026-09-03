package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeRemoteOrigin(t *testing.T) {
	for input, want := range map[string]string{
		"HTTPS://User:secret@API.Example.COM:443/v1?q=token#part": "https://api.example.com",
		"https://api.example.com:8443/v1":                         "https://api.example.com:8443",
		"http://example.com:80/path":                              "http://example.com",
		"ssh://git@GitHub.com:22/org/repo":                        "ssh://github.com",
		"git@GitHub.com:org/repo.git":                             "ssh://github.com",
		"git@[2001:db8::1]:org/repo.git":                          "ssh://[2001:db8::1]",
		"s3://My-Bucket/key":                                      "s3://my-bucket",
	} {
		got, ok := NormalizeRemoteOrigin(input)
		require.True(t, ok, input)
		require.Equal(t, want, got, input)
	}
	for _, input := range []string{"", "/tmp/file", "file:///tmp/file", "C:\\temp\\file", "https://$HOST/path", "$HOST:repo"} {
		_, ok := NormalizeRemoteOrigin(input)
		require.False(t, ok, input)
	}
}

func TestRemoteRulesMatchExactOrigin(t *testing.T) {
	rules := RulesForRemoteOrigins([]string{"https://API.example.com:443/v1"})
	require.True(t, RulesAllowRemoteOrigins(rules, []string{"https://api.example.com/other"}))
	require.False(t, RulesAllowRemoteOrigins(rules, []string{"http://api.example.com"}))
	require.False(t, RulesAllowRemoteOrigins(rules, []string{"https://api.example.com:8443"}))
	require.False(t, RulesAllowRemoteOrigins(rules, []string{"https://other.example.com"}))
}

func TestMixedWriteIntentRequiresDirectoryAndRemoteRules(t *testing.T) {
	scope := WorkspaceScope("/workspace")
	intent := AccessIntent{Class: AccessWrite, Dirs: []string{"/workspace"}, RemoteOrigins: []string{"https://api.example.com/v1"}}
	dirs := RulesForDirs([]string{"/workspace"}, scope, AccessWrite)
	remote := RulesForRemoteOrigins([]string{"https://api.example.com"})
	require.False(t, RulesAllowIntent(dirs, intent, scope, nil, ReviewAuto))
	require.False(t, RulesAllowIntent(remote, intent, scope, nil, ReviewAuto))
	require.True(t, RulesAllowIntent(append(dirs, remote...), intent, scope, nil, ReviewAuto))
	require.False(t, RulesAllowIntent(append(dirs, remote...), intent, scope, nil, ReviewAlways))
}
