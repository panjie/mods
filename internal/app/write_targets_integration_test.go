//go:build integration

package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

// Opt in explicitly: this test reads the local provider configuration and
// calls its model, but only runs read-only discovery in a disposable Git repo.
// It never starts the task request or executes a push.
func TestWriteTargetsFollowupPushIntegration(t *testing.T) {
	if os.Getenv("MODS_TEST_WRITE_TARGETS_LIVE") != "1" {
		t.Skip("set MODS_TEST_WRITE_TARGETS_LIVE=1 to test inference with the configured model")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "empty-global-config"))
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"remote", "add", "origin", "https://github.com/example/inference-fixture.git"},
		{"remote", "set-url", "--push", "origin", "https://gitee.com/example/inference-fixture.git"},
		{"-c", "user.name=Inference Test", "-c", "user.email=inference@example.com", "commit", "--allow-empty", "--no-gpg-sign", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "git fixture: %s", output)
	}
	cfg, err := config.Ensure()
	require.NoError(t, err)
	cfg.BuiltinTools.Workspace = root
	cfg.BuiltinTools.Shell = true
	cfg.MCPServers = nil
	cfg.WebSearch = false
	ctx, cancel := context.WithTimeout(context.Background(), writeTargetTimeout)
	defer cancel()
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
	api, model, err := m.resolveModel(&cfg)
	require.NoError(t, err)
	configs, err := m.buildProviderConfigs(model, api)
	require.NoError(t, err)
	_, err = m.resolveThinkWithOllama(&model, &configs.Anthropic, &configs.Google, &configs.Ollama, &configs.OpenAI)
	require.NoError(t, err)
	require.NoError(t, applyHTTPProxy(&cfg, &configs.Anthropic, &configs.Google, &configs.Ollama, &configs.OpenAI))
	client, err := newStreamClient(modelProtocol(model), configs.Anthropic, configs.Google, configs.Ollama, configs.OpenAI)
	require.NoError(t, err)
	registry, err := m.buildToolRegistryForProvider(ctx, &cfg, websearch.Config{}, "push", client)
	require.NoError(t, err)
	defer registry.Close()
	history := []proto.Message{
		{Role: proto.RoleUser, Content: "请提交当前仓库的修改"},
		{Role: proto.RoleAssistant, Content: "本地提交已完成，还没有推送。仓库目录：" + root},
		{Role: proto.RoleUser, Content: "push"},
	}
	rules, _, err := inferWriteTargets(ctx, client, proto.Request{
		API: model.API, Model: model.Name, Messages: writeTargetMessages(history, root),
		Tools: writeTargetTools(registry), ToolCaller: m.writeTargetToolCaller(ctx, registry),
	}, m.reviewer.scope)
	require.NoError(t, err)
	require.True(t, RulesAllowRemoteOrigins(rules, []string{"https://gitee.com"}), "push destination missing: %s", RulesLabel(rules))
	require.False(t, RulesAllowRemoteOrigins(rules, []string{"https://github.com"}), "fetch URL must not grant remote writes")
}
