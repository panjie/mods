package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/go-shellwords"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
)

type number interface{ int64 | float64 }

var apiKeyCmdTimeout = 10 * time.Second

func (m *Mods) retry(content string, err modsError) tea.Msg {
	m.retries++
	if m.retries >= m.Config.MaxRetries {
		debug.Printf("API error: giving up after %d retries (max=%d)", m.retries, m.Config.MaxRetries)
		return err
	}
	wait := time.Millisecond * 100 * time.Duration(math.Pow(2, float64(m.retries))) //nolint:mnd
	debug.Printf("API error: retry %d/%d in %v -> %s", m.retries, m.Config.MaxRetries, wait, err.ReasonText)
	// Return a retryMsg instead of sleeping here: a synchronous time.Sleep
	// would freeze the Bubble Tea Update loop, blocking animation redraws and
	// keystrokes (including Ctrl+C) for up to several seconds during back-off.
	// Update() schedules a tea.Tick that delivers completionInput after wait.
	return retryMsg{content: content, wait: wait}
}

func (m *Mods) startCompletionCmd(content string) tea.Cmd {
	// Release any prior request's resources before starting a new one. The
	// cancel slice and the active streamRunner can both linger if a previous
	// stream is in flight (e.g. retry path); close them explicitly so HTTP
	// bodies, MCP processes, and tool-call contexts are not leaked.
	m.closeActiveRunner()
	m.cancelMu.Lock()
	cancels := m.cancelRequest
	m.cancelRequest = nil
	m.cancelMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.reasoningActive = false
	m.responseOutputStarted = false
	m.responseBoundaryPending = false
	m.Thought = ""
	m.thoughtFlushed = false

	return func() tea.Msg {
		session, err := m.buildRequestSession(content)
		if err != nil {
			return err
		}
		return session.runner.receiveCmd()()
	}
}

func (m *Mods) toolCallContext(registry *toolregistry.Registry, name string, cfg *Config) (context.Context, context.CancelFunc) {
	if registry.TimeoutPolicy(name) == toolregistry.TimeoutPolicySelf {
		return context.WithCancel(m.ctx)
	}
	return context.WithTimeout(m.ctx, cfg.MCPTimeout)
}

func (m *Mods) ensureKey(api API, defaultEnv, docsURL string) (string, error) {
	key := api.APIKey
	if key == "" && api.APIKeyEnv != "" && api.APIKeyCmd == "" {
		key = os.Getenv(api.APIKeyEnv)
	}
	if key == "" && api.APIKeyCmd != "" {
		args, err := shellwords.Parse(api.APIKeyCmd)
		if err != nil {
			return "", modsError{Err: err, ReasonText: "Failed to parse api-key-cmd"}
		}
		if len(args) == 0 {
			return "", modsError{
				Err:        newUserErrorf("api-key-cmd cannot be empty"),
				ReasonText: "Failed to parse api-key-cmd",
			}
		}
		cmdCtx, cancel := context.WithTimeout(m.ctx, apiKeyCmdTimeout)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, args[0], args[1:]...) //nolint:gosec
		HideCommandWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
				return "", modsError{
					Err:        fmt.Errorf("api-key-cmd timed out after %s", apiKeyCmdTimeout),
					ReasonText: "Cannot exec api-key-cmd",
				}
			}
			return "", modsError{
				Err:        fmt.Errorf("api-key-cmd failed: %w", err),
				ReasonText: "Cannot exec api-key-cmd",
			}
		}
		key = strings.TrimSpace(string(out))
	}
	if key == "" {
		key = os.Getenv(defaultEnv)
	}
	if key != "" {
		return key, nil
	}
	// Surface the provider's own api-key-env when configured (e.g. a custom
	// Anthropic-compatible provider with its own variable) rather than the
	// generic provider-default variable, which would mislead the user.
	envName := defaultEnv
	if api.APIKeyEnv != "" {
		envName = api.APIKeyEnv
	}
	me := modsError{
		ReasonText: fmt.Sprintf(
			"%[1]s required; set the environment variable %[1]s or update %[2]s through %[3]s.",
			m.Styles.InlineCode.Render(envName),
			m.Styles.InlineCode.Render("mods.yaml"),
			m.Styles.InlineCode.Render("mods --settings"),
		),
	}
	if api.APIKeyEnv == "" {
		// Built-in provider: point at the vendor console for obtaining a key.
		// Custom providers with their own variable have no canonical link.
		me.Err = newUserErrorf("You can grab one at %s", m.Styles.Link.Render(docsURL))
	}
	return "", me
}

func (m *Mods) callToolsCmd(runner *streamRunner, ch chan toolOperationStatusMsg) tea.Cmd {
	return func() tea.Msg {
		msg := runner.toolCallsCmd()().(streamEventMsg)
		m.clearToolOperationChannel(ch)
		return msg
	}
}

func (m *Mods) pollToolOperationStatusCmd(ch <-chan toolOperationStatusMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return toolOperationStatusMsg{done: true}
		}
		msg.ch = ch
		return msg
	}
}

func (m *Mods) setActiveOperation(op string) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	m.activeOperation = op
}

func (m *Mods) getActiveOperation() string {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	return m.activeOperation
}

func (m *Mods) setToolOperationChannel(ch chan<- toolOperationStatusMsg) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	m.toolOperations = ch
}

func (m *Mods) clearToolOperationChannel(ch chan toolOperationStatusMsg) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	if m.toolOperations != ch {
		return
	}
	close(ch)
	m.toolOperations = nil
}

func (m *Mods) sendToolOperationStatus(content string) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	if m.toolOperations == nil {
		return
	}
	select {
	case m.toolOperations <- toolOperationStatusMsg{content: content}:
	default:
	}
}

func (m *Mods) resolveModel(cfg *Config) (API, Model, error) {
	for _, api := range cfg.APIs {
		if api.Name != cfg.API && cfg.API != "" {
			continue
		}
		for name, mod := range api.Models {
			if name == cfg.Model || slices.Contains(mod.Aliases, cfg.Model) {
				cfg.Model = name
				break
			}
		}
		mod, ok := api.Models[cfg.Model]
		if ok {
			mod.Name = cfg.Model
			mod.API = api.Name
			// An explicit api-type overrides name-based routing so a custom
			// provider can declare the adapter/protocol it speaks (e.g. an
			// Anthropic Messages API gateway), independent of its name. Only a
			// recognized, non-openai type overrides: "openai" is a true no-op
			// (keeps the provider name), and an unknown value falls back to the
			// name (OpenAI-compatible default) with a debug warning rather than
			// leaving a bogus value in mod.API.
			if api.APIType != "" {
				t := strings.ToLower(api.APIType)
				switch {
				case t == "openai":
					// no-op: name-based routing already defaults to OpenAI-compatible
				case knownAPITypes[t]:
					mod.API = t
				default:
					debug.Printf("api-type %q on %s is not a recognized adapter; using OpenAI-compatible",
						api.APIType, api.Name)
				}
			}
			return api, mod, nil
		}
		if cfg.API != "" {
			return API{}, Model{}, modsError{
				Err: newUserErrorf(
					"Available models are: %s",
					strings.Join(slices.Collect(maps.Keys(api.Models)), ", "),
				),
				ReasonText: fmt.Sprintf(
					"The API endpoint %s does not contain the model %s",
					m.Styles.InlineCode.Render(cfg.API),
					m.Styles.InlineCode.Render(cfg.Model),
				),
			}
		}
	}

	return API{}, Model{}, modsError{
		ReasonText: fmt.Sprintf(
			"Model %s is not in the settings file.",
			m.Styles.InlineCode.Render(cfg.Model),
		),
		Err: newUserErrorf(
			"Please specify an API endpoint with %s or configure the model in the settings: %s",
			m.Styles.InlineCode.Render("--api"),
			m.Styles.InlineCode.Render("mods --settings"),
		),
	}
}

// oSeriesRe matches OpenAI o-series model names: o + single digit + hyphen or end of string.
// Examples: "o1", "o1-mini", "o3-2025-04-16". Does NOT match "o10-" or custom names like "o1-finetune-v2".
var oSeriesRe = regexp.MustCompile(`^o[1-9](-|$)`)

func isOSeries(model string) bool {
	return oSeriesRe.MatchString(model)
}

func debugRequest(cfg *Config, mod *Model, messages *[]proto.Message, tools []proto.ToolSpec, request *proto.Request) {
	if !debug.Enabled() {
		return
	}
	debug.Printf("API request -> model=%s, api=%s", mod.Name, mod.API)
	debug.Printf("Request: %d message(s), %d tool definition(s)", len(*messages), len(tools))
	tempStr := "unset"
	if request.Temperature != nil {
		tempStr = fmt.Sprintf("%.2f", *request.Temperature)
	}
	maxTokStr := "unset"
	if request.MaxTokens != nil {
		maxTokStr = fmt.Sprintf("%d", *request.MaxTokens)
	}
	debug.Printf("Request: temp=%s, max_tokens=%s", tempStr, maxTokStr)
	debug.Printf("Request: no-limit=%v, max-input-chars=%d", cfg.NoLimit, mod.MaxChars)
	if cfg.HTTPProxy != "" {
		debug.Printf("HTTP proxy: %s", cfg.HTTPProxy)
	}
	if b, err := json.Marshal(request.Messages); err == nil {
		toolBody, _ := json.Marshal(request.Tools)
		debug.Printf("Request: ~%dKB body (%dKB messages + %dKB tools)",
			(len(b)+len(toolBody))/1024, len(b)/1024, len(toolBody)/1024)
	}
}

func ptrOrNil[T number](t T) *T {
	if t < 0 {
		return nil
	}
	return &t
}
