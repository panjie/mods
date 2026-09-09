package cli

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func setupFixture(t *testing.T, run func(*setupModel)) {
	t.Helper()
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{API: "ollama", Model: "llama3.1", APIs: []API{{Name: "ollama", BaseURL: "http://localhost:11434", Models: map[string]Model{"llama3.1": {}}}}}}, func() {
		m := newSetupModel()
		m.standardPath = filepath.Join(t.TempDir(), "mods.yml")
		m.portablePath = filepath.Join(t.TempDir(), "mods.yml")
		defer m.stopRequest()
		run(m)
	})
}
func setupPress(m *setupModel, code rune, mod tea.KeyMod) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code, Mod: mod})
	return cmd
}

func TestSetupConditionalPagesAndDefaults(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		require.NotContains(t, m.pages(), setupCredentials)
		require.NotContains(t, m.pages(), setupAuth)
		m.provider = addProviderOption
		m.newName = "my-gateway"
		require.Contains(t, m.pages(), setupName)
		require.Contains(t, m.pages(), setupProtocol)
		require.Contains(t, m.pages(), setupCredentials)
		m.draft().storage = "config"
		require.Contains(t, m.pages(), setupKey)
		m.data.webSearchOn = true
		m.data.webSearchProvider = "custom"
		require.Contains(t, m.pages(), setupSearchURL)
		require.NotContains(t, m.pages(), setupSearchCredentials)
		m.data.webSearchProvider = "tavily"
		m.data.webSearchKeyStorage = "config"
		require.Contains(t, m.pages(), setupSearchCredentials)
		require.Contains(t, m.pages(), setupSearchKey)
		m.data.webSearchOn = false
		require.NotContains(t, m.pages(), setupSearchProvider)
		m.provider = "github-copilot"
		require.Contains(t, m.pages(), setupAuth)
		require.NotContains(t, m.pages(), setupKey)
		m.portablePath = ""
		require.NotContains(t, m.pages(), setupStorage)
		m.enter(setupShell)
		require.Equal(t, "off", m.options()[m.cursor].Value)
	})
}
func TestSetupBackNavigationAndDraftIsolation(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.enter(setupEndpoint)
		m.input.SetValue("http://ollama.example")
		m.setValue(m.input.Value())
		m.draft().manual = "local-one\nlocal-two"
		m.draft().defaultModel = "local-two"
		m.draft().selected = []string{"local-two"}
		setupPress(m, tea.KeyEsc, 0)
		require.Equal(t, setupProvider, m.page)
		m.provider = "anthropic"
		m.enter(setupEndpoint)
		m.setValue("https://anthropic.example")
		m.draft().key = "secret"
		m.draft().manual = "claude-one"
		m.provider = "ollama"
		m.enter(setupEndpoint)
		require.Equal(t, "http://ollama.example", m.input.Value())
		require.Equal(t, "local-one\nlocal-two", m.draft().manual)
		require.Equal(t, "local-two", m.draft().defaultModel)
		require.Empty(t, m.draft().key)
		m.enter(setupStorage)
		setupPress(m, tea.KeyTab, tea.ModShift)
		require.Equal(t, setupReview, m.page)
		m.provider = addProviderOption
		m.newName = ""
		m.enter(setupName)
		setupPress(m, tea.KeyEnter, 0)
		require.Error(t, m.err)
		require.Equal(t, setupName, m.page)
		setupPress(m, tea.KeyEsc, 0)
		require.Equal(t, setupProvider, m.page)
		require.NoError(t, m.err)
	})
}
func TestSetupCancelNeverWrites(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		calls := 0
		m.save = func(string, string, configWizardSaveData) error { calls++; return nil }
		setupPress(m, tea.KeyEsc, 0)
		require.True(t, m.exitArmed)
		require.False(t, m.canceled)
		setupPress(m, tea.KeyDown, 0)
		require.False(t, m.exitArmed)
		setupPress(m, tea.KeyEsc, 0)
		setupPress(m, tea.KeyEsc, 0)
		require.True(t, m.canceled)
		require.Zero(t, calls)
		require.NoFileExists(t, m.standardPath)
	})
	setupFixture(t, func(m *setupModel) {
		m.enter(setupSummary)
		setupPress(m, 'c', tea.ModCtrl)
		require.True(t, m.canceled)
		require.NoFileExists(t, m.standardPath)
	})
}
func TestSetupDiscoveryAllModelsFilteringAndManualFallback(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/api/tags", r.URL.Path)
			fmt.Fprint(w, `{"models":[`)
			for i := 0; i < 250; i++ {
				if i > 0 {
					fmt.Fprint(w, ",")
				}
				fmt.Fprintf(w, `{"name":"model-%03d"}`, i)
			}
			fmt.Fprint(w, "]}")
		}))
		defer server.Close()
		m.draft().endpoint = server.URL
		cmd := m.enter(setupDiscovery)
		require.NotNil(t, cmd)
		m.Update(cmd())
		require.Len(t, m.options(), 250)
		m.filtering = true
		m.filter.SetValue("249")
		require.Len(t, m.filtered(), 1)
		setupPress(m, tea.KeySpace, 0)
		require.Equal(t, []string{"model-249"}, m.draft().selected)
		setupPress(m, tea.KeyEsc, 0)
		require.False(t, m.filtering)
		require.Equal(t, setupDiscovery, m.page)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, setupDefault, m.page)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, "model-249", m.draft().defaultModel)
		m.enter(setupDiscovery)
		setupPress(m, 'm', 0)
		require.Equal(t, setupManual, m.page)
		require.Empty(t, m.draft().selected)
		m.Update(tea.PasteMsg{Content: "手动-model\nother"})
		require.Contains(t, m.draft().manual, "手动-model")
	})
}
func TestSetupDiscoveryErrorAndStaleResult(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.page = setupDiscovery
		_, result := m.begin("Discovering models")
		result.err = errors.New("offline")
		m.Update(result)
		require.Error(t, m.err)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, setupManual, m.page)
		m.page = setupDiscovery
		_, old := m.begin("Discovering models")
		old.models = []string{"wrong-model"}
		old.endpoints = map[string]string{"wrong-model": "responses"}
		m.enter(setupEndpoint)
		m.setValue("http://different.example")
		m.Update(old)
		require.Empty(t, m.draft().models)
		require.Empty(t, m.draft().endpoints)
		m.provider = "anthropic"
		m.Update(old)
		require.Empty(t, m.draft().models)
	})
}
func TestSetupNetworkCancellation(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		started := make(chan struct{})
		canceled := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
		defer server.Close()
		m.draft().endpoint = server.URL
		cmd := m.enter(setupDiscovery)
		result := make(chan tea.Msg, 1)
		go func() { result <- cmd() }()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("request did not start")
		}
		setupPress(m, tea.KeyEsc, 0)
		select {
		case <-canceled:
		case <-time.After(time.Second):
			t.Fatal("request not canceled")
		}
		select {
		case msg := <-result:
			m.Update(msg)
		case <-time.After(time.Second):
			t.Fatal("command did not finish")
		}
		require.Equal(t, setupEndpoint, m.page)
		require.NoError(t, m.err)
	})
}
func TestSetupCopilotLoginAndCancellation(t *testing.T) {
	oldStart, oldPoll := startCopilotDeviceFlow, pollCopilotDeviceFlow
	defer func() { startCopilotDeviceFlow = oldStart; pollCopilotDeviceFlow = oldPoll }()
	startCopilotDeviceFlow = func(context.Context) (copilotDeviceCode, error) {
		return copilotDeviceCode{UserCode: "ABCD-EFGH", VerificationURI: "https://github.com/login/device"}, nil
	}
	pollCopilotDeviceFlow = func(context.Context, copilotDeviceCode) (string, error) { return "github-token", nil }
	setupFixture(t, func(m *setupModel) {
		m.provider = "github-copilot"
		m.enter(setupAuth)
		first := setupPress(m, tea.KeyEnter, 0)
		_, poll := m.Update(first())
		require.NotNil(t, poll)
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "ABCD-EFGH")
		require.Contains(t, view, "https://github.com/login/device")
		result := poll()
		m.Update(result)
		require.Equal(t, "github-token", m.draft().key)
		require.Equal(t, "config", m.draft().storage)
		require.Equal(t, setupDiscovery, m.page)
		require.NoFileExists(t, m.standardPath)
	})
	setupFixture(t, func(m *setupModel) {
		stopped := make(chan struct{})
		pollCopilotDeviceFlow = func(ctx context.Context, _ copilotDeviceCode) (string, error) {
			<-ctx.Done()
			close(stopped)
			return "", ctx.Err()
		}
		m.provider = "github-copilot"
		m.enter(setupAuth)
		first := setupPress(m, tea.KeyEnter, 0)
		_, poll := m.Update(first())
		done := make(chan tea.Msg, 1)
		go func() { done <- poll() }()
		setupPress(m, tea.KeyEsc, 0)
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("poll did not stop")
		}
		m.Update(<-done)
		require.Empty(t, m.draft().key)
		require.Equal(t, setupEndpoint, m.page)
	})
}
func TestSetupSaveRetryAndConnectionFailure(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.draft().manual = "llama3.1"
		m.draft().defaultModel = "llama3.1"
		attempts := 0
		m.save = func(path, previous string, data configWizardSaveData) error {
			attempts++
			require.Equal(t, m.standardPath, path)
			require.Equal(t, "llama3.1", data.modelName)
			if attempts == 1 {
				return errors.New("disk full")
			}
			return nil
		}
		m.enter(setupSummary)
		require.Zero(t, attempts)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, 1, attempts)
		require.ErrorContains(t, m.err, "disk full")
		require.False(t, m.done)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, 2, attempts)
		require.True(t, m.done)
	})
	setupFixture(t, func(m *setupModel) {
		m.provider = addProviderOption
		m.newName = "gateway"
		m.draft().manual = "model"
		m.draft().defaultModel = "model"
		m.draft().key = "private-key"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
		defer server.Close()
		m.draft().endpoint = server.URL
		calls := 0
		m.save = func(string, string, configWizardSaveData) error { calls++; return nil }
		m.enter(setupSummary)
		cmd := setupPress(m, tea.KeyEnter, 0)
		m.Update(cmd())
		require.Equal(t, setupConnectionFailed, m.page)
		require.Equal(t, "no", m.options()[m.cursor].Value)
		require.Zero(t, calls)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, setupSummary, m.page)
		require.Zero(t, calls)
		m.enter(setupConnectionFailed)
		setupPress(m, tea.KeyDown, 0)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, 1, calls)
		require.True(t, m.done)
	})
}
func TestSetupLayoutThemeAndInputs(t *testing.T) {
	for _, theme := range []string{"charm", "dracula", "catppuccin", "base16", "unknown"} {
		for _, dark := range []bool{false, true} {
			for _, size := range [][2]int{{30, 12}, {80, 24}, {120, 40}} {
				t.Run(fmt.Sprintf("%s/%t/%v", theme, dark, size), func(t *testing.T) {
					setupFixture(t, func(m *setupModel) {
						config.Theme = theme
						m.isDark = dark
						m.backgroundKnown = true
						m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
						for _, page := range []setupPage{setupProvider, setupEndpoint, setupKey, setupManual, setupDiscovery, setupAuth, setupConnectionFailed, setupSummary} {
							m.page = page
							m.prepare()
							if page == setupKey {
								m.input.SetValue("very-secret-key")
							}
							m.err = errors.New("Example failure")
							view := ansi.Strip(m.View().Content)
							require.LessOrEqual(t, lipgloss.Height(view), size[1])
							for _, line := range strings.Split(view, "\n") {
								require.LessOrEqual(t, lipgloss.Width(line), size[0]-1, line)
							}
							require.NotContains(t, view, "very-secret-key")
						}
						m.enter(setupEndpoint)
						m.Update(tea.PasteMsg{Content: "https://中文.example/" + strings.Repeat("path", 40)})
						original := m.input.Value()
						m.Update(tea.BackgroundColorMsg{Color: color.White})
						require.Equal(t, original, m.input.Value())
						require.False(t, m.isDark)
						m.Update(tea.WindowSizeMsg{Width: 10, Height: 3})
						require.Equal(t, original, m.input.Value())
						setupPress(m, 'c', tea.ModCtrl)
						require.True(t, m.canceled)
					})
				})
			}
		}
	}
}
func TestSetupPickerOnlyCommitsOnCompletion(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.picker = true
		m.apis = []setupOption{{"First", "one"}, {"Second", "two"}}
		m.pickerModels = map[string][]setupOption{"one": {{"Model A", "a"}}, "two": {{"Model B", "b"}}}
		m.provider = "one"
		m.prepare()
		setupPress(m, tea.KeyDown, 0)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, "two", m.provider)
		require.Equal(t, setupDefault, m.page)
		require.Equal(t, "ollama", config.API)
		setupPress(m, tea.KeyEnter, 0)
		require.True(t, m.done)
		require.Equal(t, "b", m.draft().defaultModel)
	})
}
func TestSetupProtocolAndSaveMetadata(t *testing.T) {
	for _, protocol := range []string{"openai", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			setupFixture(t, func(m *setupModel) {
				m.provider = addProviderOption
				m.newName = "gateway"
				d := m.draft()
				d.protocol = protocol
				d.manual = "my-model"
				d.defaultModel = "my-model"
				d.endpoint = "https://gateway.example/v1"
				d.key = "my-key"
				d.storage = "config"
				data, err := m.saveData()
				require.NoError(t, err)
				require.Equal(t, protocol, data.apiType)
				require.NoError(t, writeSetupConfig(m.standardPath, "", data))
				saved := loadCLIConfig(t, m.standardPath)
				require.Equal(t, "gateway", saved["default-api"])
				bytes, err := os.ReadFile(m.standardPath)
				require.NoError(t, err)
				require.Contains(t, string(bytes), "my-model")
				require.True(t, slices.Contains(m.pages(), setupProtocol))
			})
		})
	}
}

func TestSetupSummaryUsesEffectiveValuesWithoutSecrets(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.provider = "github-copilot"
		m.draft().key = "saved-github-token"
		m.draft().storage = "config"
		m.draft().defaultModel = "gpt-5"
		m.draft().manual = "gpt-5"
		view := m.summary()
		require.Contains(t, view, "saved in config")
		require.NotContains(t, view, "saved-github-token")
		require.NotContains(t, view, "Key environment")
		m.provider = addProviderOption
		m.newName = "acme-claude"
		m.draft().protocol = "anthropic"
		m.draft().endpoint = "https://acme.example/v1"
		m.draft().key = "private-secret"
		view = m.summary()
		require.Contains(t, view, "API type: anthropic")
		require.Contains(t, view, "https://acme.example/v1")
		require.NotContains(t, view, "private-secret")
		m.provider = "ollama"
		m.draft().endpoint = ""
		require.Equal(t, "http://localhost:11434", m.endpoint())
	})
}
func TestSetupDefaultModelTracksCurrentCandidates(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		d := m.draft()
		d.selected = []string{"first", "second"}
		d.defaultModel = "second"
		m.enter(setupDefault)
		require.Equal(t, 1, m.cursor)
		d.selected = []string{"first"}
		m.enter(setupDefault)
		require.Equal(t, "first", d.defaultModel)
		m.provider = "anthropic"
		require.Empty(t, m.draft().defaultModel)
		m.provider = "ollama"
		require.Equal(t, "first", m.draft().defaultModel)
	})
}

func TestSetupLongContentAndFilteringStayAccessible(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.Update(tea.WindowSizeMsg{Width: 30, Height: 12})
		m.standardPath = "/" + strings.Repeat("long-folder/", 20) + "tail.yml"
		m.enter(setupStorage)
		require.Contains(t, ansi.Strip(m.View().Content), "Standard")
		found := false
		for range 30 {
			setupPress(m, tea.KeyPgDown, 0)
			if strings.Contains(ansi.Strip(m.View().Content), "tail.yml") {
				found = true
				break
			}
		}
		require.True(t, found, "the entire path must be reachable by scrolling")
		setupPress(m, tea.KeyDown, 0)
		require.False(t, m.manualScroll)
		setupPress(m, '/', 0)
		m.Update(tea.PasteMsg{Content: "Portable"})
		view := ansi.Strip(m.View().Content)
		require.Contains(t, view, "/ Portable")
		require.LessOrEqual(t, lipgloss.Height(view), 12)
		m.enter(setupConnectionFailed)
		m.err = errors.New("a long connection failure with details")
		setupPress(m, tea.KeyDown, 0)
		require.Contains(t, ansi.Strip(m.View().Content), "Save configuration")
	})
}
func TestSetupCompletedCommitCannotBecomeCanceled(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.save = func(string, string, configWizardSaveData) error { return nil }
		m.enter(setupSummary)
		setupPress(m, tea.KeyEnter, 0)
		require.True(t, m.done)
		setupPress(m, 'c', tea.ModCtrl)
		require.False(t, m.canceled)
	})
}
func TestSetupCleanupWarningDoesNotRepeatSuccessfulWrite(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		calls := 0
		m.save = func(string, string, configWizardSaveData) error {
			calls++
			return &setupCleanupWarning{errors.New("old file could not be removed")}
		}
		m.enter(setupSummary)
		setupPress(m, tea.KeyEnter, 0)
		require.True(t, m.done)
		require.Len(t, m.warnings, 1)
		setupPress(m, tea.KeyEnter, 0)
		require.Equal(t, 1, calls)
	})
}

func TestSetupOptionFocusKeepsLabelAndWrappingColumns(t *testing.T) {
	for _, theme := range []string{"charm", "dracula", "catppuccin", "base16"} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", theme, dark), func(t *testing.T) {
				setupFixture(t, func(m *setupModel) {
					config.Theme = theme
					m.isDark = dark
					m.backgroundKnown = true
					for _, multi := range []bool{false, true} {
						for _, width := range []int{23, 80} {
							label := "kimi-k2.7-code-highspeed 中文-model"
							normal := setupOptionLines(m.styles(), width, label, false, multi, false)
							focused := setupOptionLines(m.styles(), width, label, true, multi, false)
							checked := setupOptionLines(m.styles(), width, label, true, multi, true)
							require.Len(t, focused, len(normal))
							require.Len(t, checked, len(normal))
							gutter := 2
							if multi {
								gutter = 6
							}
							for i, line := range normal {
								plain := ansi.Strip(line)
								focus := ansi.Strip(focused[i])
								check := ansi.Strip(checked[i])
								require.Equal(t, plain[gutter:], focus[gutter:], "focus must not move or rewrap label text")
								require.Equal(t, plain[gutter:], check[gutter:], "checking must not move label text")
								require.Equal(t, lipgloss.Width(plain), lipgloss.Width(focus))
								require.LessOrEqual(t, lipgloss.Width(focus), width)
							}
						}
					}
				})
			})
		}
	}
}

func TestSetupUsesCompactSharedPanelAndStableOptionColumns(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.page = setupDiscovery
		m.draft().discovered = true
		m.draft().models = []string{"kimi-k2.6", "kimi-k2.7-code", "kimi-k3"}
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		before := ansi.Strip(m.View().Content)
		require.Contains(t, before, "DISCOVER MODELS")
		require.Contains(t, before, "┃")
		require.NotContains(t, before, "╭")
		require.Less(t, lipgloss.Height(before), 20, "short pages must not fill the terminal with empty space")
		columns := func(view string) map[string]int {
			out := map[string]int{}
			for _, line := range strings.Split(view, "\n") {
				for _, label := range m.draft().models {
					if i := strings.Index(line, label); i >= 0 {
						out[label] = lipgloss.Width(line[:i])
					}
				}
			}
			return out
		}
		positions := columns(before)
		require.Len(t, positions, 3)
		require.Equal(t, positions["kimi-k2.6"], positions["kimi-k2.7-code"])
		setupPress(m, tea.KeyDown, 0)
		require.Equal(t, positions, columns(ansi.Strip(m.View().Content)))
		setupPress(m, tea.KeySpace, 0)
		require.Equal(t, positions, columns(ansi.Strip(m.View().Content)))
	})
}

func TestSetupVimNavigationAndQuit(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.page = setupDiscovery
		m.draft().models = []string{"first", "second", "third"}
		setupPress(m, 'k', 0)
		require.Zero(t, m.cursor)
		setupPress(m, 'j', 0)
		require.Equal(t, 1, m.cursor)
		setupPress(m, 'j', 0)
		setupPress(m, 'j', 0)
		require.Equal(t, 2, m.cursor)
		setupPress(m, 'k', 0)
		require.Equal(t, 1, m.cursor)
		setupPress(m, tea.KeySpace, 0)
		require.Equal(t, []string{"second"}, m.draft().selected)
		ctx, result := m.begin("Discovering models")
		result.models = []string{"late"}
		m.Update(tea.WindowSizeMsg{Width: 10, Height: 3})
		require.NotNil(t, setupPress(m, 'q', 0))
		require.True(t, m.canceled)
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		m.Update(result)
		require.Equal(t, []string{"first", "second", "third"}, m.draft().models)
		require.NoFileExists(t, m.standardPath)
	})
}

func TestSetupVimLettersRemainEditable(t *testing.T) {
	for _, page := range []setupPage{setupName, setupEndpoint, setupKey, setupManual, setupProvider} {
		t.Run(fmt.Sprint(page), func(t *testing.T) {
			setupFixture(t, func(m *setupModel) {
				m.enter(page)
				m.input.SetValue("")
				m.area.SetValue("")
				if page == setupProvider {
					m.filtering = true
					m.filter.Focus()
				}
				for _, r := range "jkq" {
					m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				}
				require.False(t, m.canceled)
				switch page {
				case setupManual:
					require.Equal(t, "jkq", m.area.Value())
				case setupProvider:
					require.Equal(t, "jkq", m.filter.Value())
				default:
					require.Equal(t, "jkq", m.input.Value())
				}
			})
		})
	}
}
