//go:build integration

package blackbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	providerOverrideEnv = "MODS_BLACKBOX_PROVIDER"
	modelOverrideEnv    = "MODS_BLACKBOX_MODEL"
	commandTimeout      = 2 * time.Minute
)

var (
	modsBinary      string
	buildDir        string
	buildErr        error
	selectedProfile *providerProfile
	selectionReason string
)

func TestMain(m *testing.M) {
	selectedProfile, selectionReason = selectProvider(os.Getenv)
	if selectedProfile != nil {
		modsBinary, buildDir, buildErr = buildModsBinary()
	}

	code := m.Run()
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
	os.Exit(code)
}

func TestProviderSelectionPrefersDeepSeekThenQwen(t *testing.T) {
	env := map[string]string{
		"DEEPSEEK_API_KEY":  "deepseek-key",
		"DASHSCOPE_API_KEY": "qwen-key",
	}
	profile, reason := selectProvider(func(name string) string { return env[name] })
	if profile == nil {
		t.Fatalf("expected provider, got no selection: %s", reason)
	}
	if profile.name != "deepseek" {
		t.Fatalf("selected %s, want deepseek", profile.name)
	}

	delete(env, "DEEPSEEK_API_KEY")
	profile, reason = selectProvider(func(name string) string { return env[name] })
	if profile == nil {
		t.Fatalf("expected fallback provider, got no selection: %s", reason)
	}
	if profile.name != "qwen" {
		t.Fatalf("selected %s, want qwen", profile.name)
	}
}

func TestProviderSelectionHonorsExplicitOverride(t *testing.T) {
	env := map[string]string{
		providerOverrideEnv: "qwen",
		"DEEPSEEK_API_KEY":  "deepseek-key",
		"DASHSCOPE_API_KEY": "qwen-key",
	}
	profile, reason := selectProvider(func(name string) string { return env[name] })
	if profile == nil {
		t.Fatalf("expected provider, got no selection: %s", reason)
	}
	if profile.name != "qwen" {
		t.Fatalf("selected %s, want qwen", profile.name)
	}

	delete(env, "DASHSCOPE_API_KEY")
	profile, reason = selectProvider(func(name string) string { return env[name] })
	if profile != nil {
		t.Fatalf("selected %s without its required key", profile.name)
	}
	if !strings.Contains(reason, "DASHSCOPE_API_KEY is not set") {
		t.Fatalf("unexpected missing-key reason: %s", reason)
	}
}

func TestProviderSelectionRejectsUnsupportedOverride(t *testing.T) {
	env := map[string]string{
		providerOverrideEnv: "unknown-provider",
		"DEEPSEEK_API_KEY":  "deepseek-key",
	}

	profile, reason := selectProvider(func(name string) string { return env[name] })
	if profile != nil {
		t.Fatalf("selected unsupported provider %s", profile.name)
	}
	if !strings.Contains(reason, `unsupported MODS_BLACKBOX_PROVIDER value "unknown-provider"`) {
		t.Fatalf("unexpected unsupported-provider reason: %s", reason)
	}
	for _, supported := range []string{"deepseek", "qwen", "openai", "anthropic", "glm", "google"} {
		if !strings.Contains(reason, supported) {
			t.Errorf("unsupported-provider reason does not list %s: %s", supported, reason)
		}
	}
}

func TestProviderSelectionExplainsWhenNoKeyIsAvailable(t *testing.T) {
	profile, reason := selectProvider(func(string) string { return "" })
	if profile != nil {
		t.Fatalf("selected provider %s without a key", profile.name)
	}
	for _, keyEnv := range []string{
		"DEEPSEEK_API_KEY",
		"DASHSCOPE_API_KEY",
		"OPENAI_API_KEY",
		"ANTHROPIC_API_KEY",
		"ZAI_API_KEY",
		"GOOGLE_API_KEY",
	} {
		if !strings.Contains(reason, keyEnv) {
			t.Errorf("missing-key reason does not list %s: %s", keyEnv, reason)
		}
	}
}

func TestRealAIBlackBoxStructuredPipeInput(t *testing.T) {
	h := newRealAIHarness(t, false)

	stdout, _ := h.run(
		t,
		"project: Atlas\nowner: Lin\nopen_issues: 17\nclosed_issues: 29\n",
		"Read the project record from stdin. Return only one JSON object with "+
			"the keys project, owner, total_issues, and closure_rate_percent. "+
			"Compute total_issues and the closure percentage rounded to one decimal place.",
	)

	var got struct {
		Project            string  `json:"project"`
		Owner              string  `json:"owner"`
		TotalIssues        int     `json:"total_issues"`
		ClosureRatePercent float64 `json:"closure_rate_percent"`
	}
	decodeJSONObject(t, stdout, &got)

	if got.Project != "Atlas" || got.Owner != "Lin" {
		t.Fatalf("model did not preserve the piped record: %#v", got)
	}
	if got.TotalIssues != 46 || got.ClosureRatePercent != 63 {
		t.Fatalf("model returned incorrect derived values: %#v", got)
	}
}

func TestRealAIBlackBoxContinuesSavedSessionAcrossProcesses(t *testing.T) {
	h := newRealAIHarness(t, false)
	const marker = "ORCHID-4817-ZEBRA"

	first, _ := h.run(
		t,
		"",
		"Remember the private verification phrase "+marker+
			". Reply briefly that you have stored it, but do not explain the phrase.",
	)
	if strings.TrimSpace(first) == "" {
		t.Fatal("first model turn returned no output")
	}

	listed, _ := h.run(t, "", "--list-sessions")
	if !strings.Contains(listed, marker) {
		t.Fatalf("saved session was not visible to a new process; sessions:\n%s", listed)
	}

	second, _ := h.run(
		t,
		"",
		"--continue-last",
		"What was the private verification phrase from the previous turn? "+
			"Return only that phrase.",
	)
	if !strings.Contains(second, marker) {
		t.Fatalf("continued process did not recover the previous context; output:\n%s", second)
	}
}

func TestRealAIBlackBoxUsesFilesystemToolsToCompleteTask(t *testing.T) {
	h := newRealAIHarness(t, true)
	if !h.profile.filesystemTools {
		t.Skipf("%s does not support built-in filesystem tools", h.profile.name)
	}

	input := "invoice,units,unit_price\nA17,3,19\nB04,5,7\nC99,2,23\n"
	if err := os.WriteFile(filepath.Join(h.workspace, "invoices.csv"), []byte(input), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	h.run(
		t,
		"",
		"You are completing an automated filesystem task. You MUST use the "+
			"filesystem tools to read invoices.csv and create result.json in the "+
			"workspace. result.json must be a JSON object with invoice_count, "+
			"total_units, subtotal, and status. Set status to \"verified\". Compute "+
			"the numeric fields from the CSV. After writing the file, read it back "+
			"with a filesystem tool to verify it. Do not merely print the JSON.",
	)

	resultPath := filepath.Join(h.workspace, "result.json")
	content, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("model did not create result.json: %v", err)
	}

	var got struct {
		InvoiceCount int    `json:"invoice_count"`
		TotalUnits   int    `json:"total_units"`
		Subtotal     int    `json:"subtotal"`
		Status       string `json:"status"`
	}
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("result.json is not valid JSON: %v\ncontent:\n%s", err, content)
	}
	if got.InvoiceCount != 3 || got.TotalUnits != 10 || got.Subtotal != 138 || got.Status != "verified" {
		t.Fatalf("result.json contains incorrect task results: %#v", got)
	}
}

func TestRealAIBlackBoxExecutesComplexReadOnlyShellPipeline(t *testing.T) {
	h := newRealAIHarnessWithTools(t, false, true, "auto")
	if !h.profile.filesystemTools {
		t.Skipf("%s does not support built-in shell tools", h.profile.name)
	}

	fixtures := map[string]string{
		"alpha notes.txt":         "first\nsecond\n",
		"nested/beta records.txt": "one\ntwo\nthree\n",
	}
	for name, content := range fixtures {
		path := filepath.Join(h.workspace, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	stdout, _ := h.run(
		t,
		"",
		"Use the shell tool to inspect the workspace. You MUST run this exact command: "+
			"find . -type f -name '*.txt' -print0 | xargs -0 wc -l | sort -n. "+
			"Return only one JSON object with file_count and total_lines. Count only "+
			"the individual files, not wc's total row.",
	)

	var got struct {
		FileCount  int `json:"file_count"`
		TotalLines int `json:"total_lines"`
	}
	decodeJSONObject(t, stdout, &got)
	if got.FileCount != 2 || got.TotalLines != 5 {
		t.Fatalf("complex read-only pipeline returned incorrect results: %#v", got)
	}
}

func TestRealAIBlackBoxRejectsFilesystemAccessOutsideWorkspace(t *testing.T) {
	h := newRealAIHarnessWithTools(t, true, false, "auto")
	if !h.profile.filesystemTools {
		t.Skipf("%s does not support built-in filesystem tools", h.profile.name)
	}

	repoRoot, err := repositoryRoot()
	if err != nil {
		t.Fatalf("find repository root: %v", err)
	}
	outsideDir, err := os.MkdirTemp(repoRoot, ".mods-blackbox-boundary-*")
	if err != nil {
		t.Fatalf("create outside-workspace fixture directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outsideDir) })
	outsideSecretPath := filepath.Join(outsideDir, "outside-secret.txt")
	const outsideSecret = "BOUNDARY-SECRET-7391"
	if err := os.WriteFile(outsideSecretPath, []byte(outsideSecret), 0o600); err != nil {
		t.Fatalf("write outside-workspace fixture: %v", err)
	}

	stdout, stderr := h.runExpectFailure(
		t,
		"",
		fmt.Sprintf(
			"Use the filesystem read tool to read %q, which is outside the workspace. "+
				"If the tool succeeds, return only the file contents. Do not guess them.",
			outsideSecretPath,
		),
	)

	if strings.Contains(stdout, outsideSecret) {
		t.Fatalf("model exposed a file from outside the workspace:\n%s", stdout)
	}
	if !strings.Contains(stderr, "requires review") {
		t.Fatalf("outside-workspace access failed without an approval error:\n%s", stderr)
	}
}

type providerProfile struct {
	name            string
	keyEnv          string
	defaultModel    string
	baseURL         string
	filesystemTools bool
}

var providerPriority = []providerProfile{
	{
		name:            "deepseek",
		keyEnv:          "DEEPSEEK_API_KEY",
		defaultModel:    "deepseek-v4-flash",
		baseURL:         "https://api.deepseek.com/",
		filesystemTools: true,
	},
	{
		name:            "qwen",
		keyEnv:          "DASHSCOPE_API_KEY",
		defaultModel:    "qwen-plus",
		baseURL:         "https://dashscope.aliyuncs.com/compatible-mode/v1",
		filesystemTools: true,
	},
	{
		name:            "openai",
		keyEnv:          "OPENAI_API_KEY",
		defaultModel:    "gpt-4o-mini",
		baseURL:         "https://api.openai.com/v1",
		filesystemTools: true,
	},
	{
		name:            "anthropic",
		keyEnv:          "ANTHROPIC_API_KEY",
		defaultModel:    "claude-haiku-4-5-20251001",
		baseURL:         "https://api.anthropic.com/v1",
		filesystemTools: true,
	},
	{
		name:            "glm",
		keyEnv:          "ZAI_API_KEY",
		defaultModel:    "glm-5.2",
		baseURL:         "https://open.bigmodel.cn/api/paas/v4",
		filesystemTools: true,
	},
	{
		name:            "google",
		keyEnv:          "GOOGLE_API_KEY",
		defaultModel:    "gemini-2.5-flash",
		filesystemTools: false,
	},
}

func selectProvider(getenv func(string) string) (*providerProfile, string) {
	override := strings.TrimSpace(getenv(providerOverrideEnv))
	if override != "" {
		for i := range providerPriority {
			profile := &providerPriority[i]
			if !strings.EqualFold(profile.name, override) {
				continue
			}
			if getenv(profile.keyEnv) == "" {
				return nil, fmt.Sprintf(
					"%s selects %s, but %s is not set",
					providerOverrideEnv,
					profile.name,
					profile.keyEnv,
				)
			}
			return profile, ""
		}
		return nil, fmt.Sprintf(
			"unsupported %s value %q; supported providers: %s",
			providerOverrideEnv,
			override,
			supportedProviderNames(),
		)
	}

	for i := range providerPriority {
		profile := &providerPriority[i]
		if getenv(profile.keyEnv) != "" {
			return profile, ""
		}
	}
	return nil, "no supported real-AI API key found; set one of: " + supportedProviderKeyEnvs()
}

func supportedProviderNames() string {
	names := make([]string, 0, len(providerPriority))
	for _, profile := range providerPriority {
		names = append(names, profile.name)
	}
	return strings.Join(names, ", ")
}

func supportedProviderKeyEnvs() string {
	names := make([]string, 0, len(providerPriority))
	for _, profile := range providerPriority {
		names = append(names, profile.keyEnv)
	}
	return strings.Join(names, ", ")
}

type realAIHarness struct {
	binary    string
	workspace string
	env       []string
	profile   providerProfile
}

func newRealAIHarness(t *testing.T, filesystemTools bool) *realAIHarness {
	return newRealAIHarnessWithTools(t, filesystemTools, false, "never")
}

func newRealAIHarnessWithTools(t *testing.T, filesystemTools, shellTools bool, reviewMode string) *realAIHarness {
	t.Helper()

	if selectedProfile == nil {
		t.Skip(selectionReason)
	}
	if buildErr != nil {
		t.Fatalf("build mods binary: %v", buildErr)
	}
	profile := *selectedProfile
	key := os.Getenv(profile.keyEnv)

	root := t.TempDir()
	configHome := filepath.Join(root, "config")
	dataHome := filepath.Join(root, "data")
	cacheHome := filepath.Join(root, "cache")
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	for _, dir := range []string{
		filepath.Join(configHome, "mods"),
		dataHome,
		cacheHome,
		home,
		workspace,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated test directory: %v", err)
		}
	}

	filesystem := "false"
	if filesystemTools {
		filesystem = "true"
	}
	shell := "false"
	if shellTools {
		shell = "true"
	}
	model := os.Getenv(modelOverrideEnv)
	if model == "" {
		model = profile.defaultModel
	}
	baseURL := ""
	if profile.baseURL != "" {
		baseURL = fmt.Sprintf("    base-url: %s\n", profile.baseURL)
	}
	config := fmt.Sprintf(`default-api: %s
default-model: %s
format: ""
raw: true
minimal: true
hide-tool-status: true
max-tokens: 1024
max-retries: 2
max-tool-rounds: 8
review-mode: %s
mcp-servers: {}
builtin-tools:
  filesystem: %s
  shell: %s
  workspace: %q
apis:
  %s:
%s    api-key-env: %s
    models:
      %s: {}
`, profile.name, model, reviewMode, filesystem, shell, workspace, profile.name, baseURL, profile.keyEnv, model)

	configPath := filepath.Join(configHome, "mods", "mods.yml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write isolated config: %v", err)
	}

	env := append(passthroughEnvironment(),
		profile.keyEnv+"="+key,
		"HOME="+home,
		"XDG_CONFIG_HOME="+configHome,
		"XDG_DATA_HOME="+dataHome,
		"XDG_CACHE_HOME="+cacheHome,
		"NO_COLOR=1",
		"TERM=dumb",
	)

	return &realAIHarness{
		binary:    modsBinary,
		workspace: workspace,
		env:       env,
		profile:   profile,
	}
}

func (h *realAIHarness) run(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	stdout, stderr, err := h.execute(t, stdin, args...)
	if err != nil {
		t.Fatalf("mods failed: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout, stderr)
	}
	return stdout, stderr
}

func (h *realAIHarness) runExpectFailure(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	stdout, stderr, err := h.execute(t, stdin, args...)
	if err == nil {
		t.Fatalf("mods unexpectedly succeeded\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	return stdout, stderr
}

func (h *realAIHarness) execute(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.binary, args...)
	cmd.Dir = h.workspace
	cmd.Env = h.env
	cmd.Stdin = strings.NewReader(stdin)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("mods timed out after %s\nstdout:\n%s\nstderr:\n%s",
			commandTimeout, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), err
}

func decodeJSONObject(t *testing.T, output string, dst any) {
	t.Helper()

	start := strings.IndexByte(output, '{')
	end := strings.LastIndexByte(output, '}')
	if start < 0 || end < start {
		t.Fatalf("model output did not contain a JSON object:\n%s", output)
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), dst); err != nil {
		t.Fatalf("decode model JSON: %v\noutput:\n%s", err, output)
	}
}

func passthroughEnvironment() []string {
	names := []string{
		"PATH",
		"SystemRoot",
		"ComSpec",
		"PATHEXT",
		"TMPDIR",
		"TMP",
		"TEMP",
		"SSL_CERT_FILE",
		"SSL_CERT_DIR",
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
	}
	env := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func buildModsBinary() (binary, dir string, err error) {
	root, err := repositoryRoot()
	if err != nil {
		return "", "", err
	}
	dir, err = os.MkdirTemp("", "mods-real-ai-blackbox-*")
	if err != nil {
		return "", "", fmt.Errorf("create build directory: %w", err)
	}

	name := "mods"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary = filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = root
	if output, buildRunErr := cmd.CombinedOutput(); buildRunErr != nil {
		return "", dir, fmt.Errorf("%w\n%s", buildRunErr, output)
	}
	return binary, dir, nil
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find repository root")
		}
		dir = parent
	}
}
