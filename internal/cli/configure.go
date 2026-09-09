package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/panjie/mods/internal/anthropic"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/copilot"
	"github.com/panjie/mods/internal/providerinfo"
	"github.com/panjie/mods/internal/ui"
)

var copilotGitHubAPIBaseURL = copilot.DefaultGitHubAPIURL

const (
	addProviderOption = "__mods_add_provider__"
	addModelOption    = "__mods_add_model__"
)

func buildProviderOptions() []setupOption {
	seen := map[string]struct{}{}
	builtins := providerinfo.Descriptors()
	opts := make([]setupOption, 0, len(config.APIs)+len(builtins)+1)
	for _, api := range config.APIs {
		if len(api.Models) == 0 {
			if _, builtIn := providerinfo.Lookup(api.Name); !builtIn {
				seen[api.Name] = struct{}{}
				opts = append(opts, newSetupOption(incompleteProviderLabel(api), api.Name))
			}
			continue
		}
		seen[api.Name] = struct{}{}
		opts = append(opts, newSetupOption(configuredProviderLabel(api), api.Name))
	}
	for _, provider := range builtins {
		if _, ok := seen[provider.Name]; ok {
			continue
		}
		opts = append(opts, newSetupOption(availableProviderLabel(provider), provider.Name))
	}
	opts = append(opts, newSetupOption("+ Add new provider", addProviderOption))
	return opts
}

func configuredProviderLabel(api API) string {
	checkMark := "✓"
	if config.NerdFontGlyphs {
		checkMark = ui.NerdMark
	}
	return fmt.Sprintf("%s %-12s  %s", checkMark, api.Name, configuredProviderModelsSummary(api))
}

func incompleteProviderLabel(api API) string {
	return fmt.Sprintf("+ %-12s  %s", api.Name, configuredProviderModelsSummary(api))
}

func availableProviderLabel(provider providerinfo.NamedDescriptor) string {
	if provider.Description == "" {
		return fmt.Sprintf("+ %-12s", provider.Name)
	}
	return fmt.Sprintf("+ %-12s  %s", provider.Name, provider.Description)
}

func configuredProviderModelsSummary(api API) string {
	names := configuredProviderModelNames(api)
	if len(names) == 0 {
		return "no models configured"
	}
	const maxShown = 3
	if len(names) <= maxShown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(names[:maxShown], ", "), len(names)-maxShown)
}

func configuredProviderModelNames(api API) []string {
	names := make([]string, 0, len(api.Models))
	for name := range api.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type copilotDeviceCode struct {
	UserCode        string
	VerificationURI string
	device          copilot.DeviceCode
}

var startCopilotDeviceFlow = func(ctx context.Context) (copilotDeviceCode, error) {
	device, err := copilot.StartDeviceFlow(ctx, copilot.Client{})
	if err != nil {
		return copilotDeviceCode{}, err
	}
	return copilotDeviceCode{
		UserCode:        device.UserCode,
		VerificationURI: device.VerificationURI,
		device:          device,
	}, nil
}

var pollCopilotDeviceFlow = func(ctx context.Context, code copilotDeviceCode) (string, error) {
	token, err := copilot.PollDeviceFlow(ctx, copilot.Client{}, code.device)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func configWizardWaitingForCopilotAuth(apiName, apiKey string) bool {
	return apiName == "github-copilot" && strings.TrimSpace(resolveKeyForDiscovery(apiName, apiKey)) == ""
}

func configWizardDiscoveryFailurePrefix(discoveryErr error) string {
	if discoveryErr == nil {
		return ""
	}
	return fmt.Sprintf("Model discovery failed: %v\n", discoveryErr)
}

func configWizardDiscoveryDescription(discoveryErr error) string {
	return configWizardDiscoveryFailurePrefix(discoveryErr) +
		"Select models to add, or press Enter to enter model names manually. You will choose the default model next."
}

func configWizardManualModelsDescription(discoveryErr error) string {
	return configWizardDiscoveryFailurePrefix(discoveryErr) +
		"Enter model identifiers here, one per line. You will choose the default model next."
}

func configWizardHideDiscoveryModels(waitingForCopilotAuth bool) bool {
	return waitingForCopilotAuth
}

func configWizardHideManualModels(waitingForCopilotAuth, discoverySucceeded bool, discoveredPick []string) bool {
	return waitingForCopilotAuth || discoverySucceeded && len(discoveredPick) > 0
}

func configWizardDiscoveryType(chosenAPI, newProviderName, apiType string) string {
	api := wizardProviderName(chosenAPI, newProviderName)
	if chosenAPI == addProviderOption {
		if providerinfo.Protocol(api, "") != "openai" {
			return api
		}
		return apiType
	}
	return api
}

func wizardProviderName(chosenAPI, newProviderName string) string {
	if chosenAPI == addProviderOption {
		return strings.TrimSpace(newProviderName)
	}
	return chosenAPI
}

func validateNewProviderName(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("provider name is required")
	}
	if value == addProviderOption || value == addModelOption {
		return fmt.Errorf("provider name is reserved")
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("provider name may only contain lowercase letters, digits, '-' or '_'")
	}
	for _, api := range config.APIs {
		if api.Name == value {
			return fmt.Errorf("provider %q already exists", value)
		}
	}
	return nil
}

func validateNewModelName(provider, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("model name is required")
	}
	if value == addProviderOption || value == addModelOption {
		return fmt.Errorf("model name is reserved")
	}
	for _, api := range config.APIs {
		if api.Name != provider {
			continue
		}
		if _, ok := api.Models[value]; ok {
			return fmt.Errorf("model %q already exists for %s", value, provider)
		}
		break
	}
	return nil
}

func parseNewModelNames(provider, value string) ([]string, error) {
	return parseModelNames(provider, value, false)
}

func parseModelNames(provider, value string, allowExisting bool) ([]string, error) {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	existing := existingModelNames(provider)
	for _, line := range strings.Split(value, "\n") {
		model := strings.TrimSpace(line)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		if _, ok := existing[model]; !(allowExisting && ok) {
			if err := validateNewModelName(provider, model); err != nil {
				return nil, err
			}
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("model name is required")
	}
	return models, nil
}

func configWizardModelNames(provider string, discoveredPick []string, manualModelsText string) ([]string, error) {
	if len(discoveredPick) > 0 {
		return discoveredPick, nil
	}
	return parseModelNames(provider, manualModelsText, true)
}

func configWizardModelOptionLabel(apiName, modelName string) string {
	if apiName == config.API && modelName == config.Model {
		return modelName + " (current default)"
	}
	return modelName
}

func configWizardPreferredDefaultModel(selected string, models []string) string {
	if slices.Contains(models, selected) {
		return selected
	}
	if len(models) == 0 {
		return ""
	}
	return models[0]
}

func validateConfigWizardDefaultModel(selected string, models []string) error {
	if !slices.Contains(models, selected) {
		return fmt.Errorf("select a default model from the models above")
	}
	return nil
}

func validateWizardBaseURL(chosenAPI, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if chosenAPI == addProviderOption {
			return fmt.Errorf("base URL is required")
		}
		return nil
	}
	if !isHTTPURL(value) {
		return fmt.Errorf("base URL must start with http:// or https://")
	}
	return nil
}

// resolveEnvVar returns the configured api-key-env for the provider, or
// generates a sensible default (UPPERCASE_API_KEY) if not set.
func resolveEnvVar(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			if api.APIKeyEnv != "" {
				return api.APIKeyEnv
			}
			break
		}
	}
	return strings.ToUpper(strings.ReplaceAll(apiName, "-", "_")) + "_API_KEY"
}

// findBaseURL returns the configured base URL for the provider.
func findBaseURL(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			return api.BaseURL
		}
	}
	return ""
}

// builtinBaseURL returns the official default endpoint for a built-in
// provider. Used as a placeholder/hint in the --config wizard and as a
// fallback for model discovery when the user has not yet configured a
// base-url. Google's default URL embeds {model} — applyGoogleBaseURLOverride
// (internal/app/provider.go) substitutes it at runtime.
func builtinBaseURL(apiName string) string {
	return providerinfo.DefaultBaseURL(apiName)
}

// findAPIType returns the configured api-type (wire protocol) for the provider,
// or "" if unset (meaning name-based routing / OpenAI-compatible default).
func findAPIType(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			return api.APIType
		}
	}
	return ""
}

// existingModelNames returns the set of model names already configured on the
// provider, so discovery can skip re-adding them.
func existingModelNames(apiName string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, api := range config.APIs {
		if api.Name != apiName {
			continue
		}
		for name := range api.Models {
			out[name] = struct{}{}
		}
		break
	}
	return out
}

type configWizardProviderCatalog struct {
	models map[string][]string
	sets   map[string]map[string]struct{}
}

func newConfigWizardProviderCatalog(cfg Config) configWizardProviderCatalog {
	catalog := configWizardProviderCatalog{
		models: make(map[string][]string, len(cfg.APIs)),
		sets:   make(map[string]map[string]struct{}, len(cfg.APIs)),
	}
	for _, api := range cfg.APIs {
		set := make(map[string]struct{}, len(api.Models))
		names := make([]string, 0, len(api.Models))
		for name := range api.Models {
			set[name] = struct{}{}
			names = append(names, name)
		}
		sort.Strings(names)
		catalog.models[api.Name] = names
		catalog.sets[api.Name] = set
	}
	return catalog
}

func (c configWizardProviderCatalog) configuredModels(apiName string) []string {
	models := c.models[apiName]
	out := make([]string, len(models))
	copy(out, models)
	return out
}

func (c configWizardProviderCatalog) existingSet(apiName string) map[string]struct{} {
	set := c.sets[apiName]
	if set == nil {
		return map[string]struct{}{}
	}
	return set
}

func (c configWizardProviderCatalog) preselectedDiscoveredModels(apiName string, discovered []string) []string {
	existing := c.existingSet(apiName)
	selected := make([]string, 0, len(existing))
	for _, modelName := range discovered {
		if _, ok := existing[modelName]; ok {
			selected = append(selected, modelName)
		}
	}
	return selected
}

func (c configWizardProviderCatalog) manualModelText(apiName string) string {
	return strings.Join(c.configuredModels(apiName), "\n")
}

func normalizeWebSearchProviderForWizard(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if isHTTPURL(provider) {
		return "custom"
	}
	switch provider {
	case "custom":
		return "custom"
	default:
		return cfgpkg.DefaultWebSearchProvider
	}
}

func webSearchCustomURLForWizard(provider string) string {
	provider = strings.TrimSpace(provider)
	if isHTTPURL(provider) {
		return provider
	}
	return ""
}

func webSearchProviderForConfig(provider, customURL string) string {
	if provider == "custom" {
		return strings.TrimSpace(customURL)
	}
	return provider
}

func webSearchProviderUsesKey(provider string) bool {
	return provider == "tavily"
}

type configWizardSaveData struct {
	apiName, apiType, modelName, reviewMode, fsMode string
	webSearchProvider, webSearchProviderValue       string
	webSearchKeyStorage, webSearchAPIKey            string
	webSearchAPIKeyEnv                              string
	keyStorage, apiKey, envVarName, baseURLInput    string
	addedModelNames                                 []string
	copilotModelEndpoints                           map[string]string
	shellOn, webSearchOn, portable                  bool
}

func buildConfigWizardUpdates(d configWizardSaveData) []FieldUpdate {
	updates := []FieldUpdate{
		{Path: []string{"default-api"}, Value: d.apiName},
		{Path: []string{"default-model"}, Value: d.modelName},
		{Path: []string{"review-mode"}, Value: d.reviewMode},
		{Path: []string{"builtin-tools", "filesystem"}, Value: d.fsMode},
		{Path: []string{"builtin-tools", "shell"}, Value: d.shellOn},
		{Path: []string{"web-search"}, Value: d.webSearchOn},
		{Path: []string{"web-search-provider"}, Value: d.webSearchProviderValue},
	}
	if d.webSearchOn && webSearchProviderUsesKey(d.webSearchProvider) {
		if d.webSearchKeyStorage == "config" {
			updates = append(updates, FieldUpdate{Path: []string{"web-search-api-key"}, Value: strings.TrimSpace(d.webSearchAPIKey)})
		} else {
			updates = append(updates, FieldUpdate{Path: []string{"web-search-api-key"}, Value: nil})
			updates = append(updates, FieldUpdate{Path: []string{"web-search-api-key-env"}, Value: d.webSearchAPIKeyEnv})
		}
	}

	if d.apiName != "ollama" {
		if d.apiName == "github-copilot" && d.apiKey == "" {
			if key := configuredAPIKey(d.apiName); key != "" {
				d.apiKey = key
				d.keyStorage = "config"
			}
		}
		if d.keyStorage == "config" && d.apiKey != "" {
			updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-key"}, Value: d.apiKey})
			updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-key-env"}, Value: nil})
		} else if d.envVarName != "" {
			updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-key"}, Value: nil})
			updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-key-env"}, Value: d.envVarName})
		}
	}

	if d.baseURLInput != "" {
		updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "base-url"}, Value: strings.TrimSpace(d.baseURLInput)})
	}

	// A newly added provider may declare a non-OpenAI protocol (e.g. an
	// Anthropic Messages API gateway). "openai" is the default and writes
	// nothing, so existing OpenAI-compatible behavior is unchanged.
	if d.apiType != "" && d.apiType != "openai" {
		updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-type"}, Value: d.apiType})
	}

	// Register each newly added model under
	// apis.<api>.models.<name>. mods treats any model listed here as
	// selectable; per-model fields (fallback, thinking-*, etc.) can be added by
	// the user later. Provider-specific routing defaults are written explicitly;
	// all other models use an empty mapping.
	// Previously curated model entries are left untouched.
	for _, modelName := range configWizardNewModelNames(d.apiName, d.addedModelNames) {
		value := configWizardNewModelSettings(d.apiName, modelName, d.baseURLInput)
		if d.apiName == "github-copilot" {
			if endpoint := d.copilotModelEndpoints[modelName]; endpoint != "" {
				value["endpoint"] = endpoint
			}
		}
		updates = append(updates, FieldUpdate{
			Path:  []string{"apis", d.apiName, "models", modelName},
			Value: value,
		})
	}

	return updates
}

func configWizardNewModelSettings(apiName, modelName, baseURL string) map[string]any {
	settings := map[string]any{}
	if strings.EqualFold(strings.TrimSpace(apiName), "deepseek") &&
		configWizardIsOfficialDeepSeekResponsesModel(modelName) &&
		configWizardIsOfficialDeepSeekURL(baseURL) {
		settings["endpoint"] = "responses"
	}
	return settings
}

func configWizardIsOfficialDeepSeekResponsesModel(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case "deepseek-v4-flash", "deepseek-v4-pro":
		return true
	}
	return false
}

func configWizardIsOfficialDeepSeekURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	return err == nil && strings.EqualFold(u.Hostname(), "api.deepseek.com")
}

func configWizardNewModelNames(apiName string, modelNames []string) []string {
	existing := existingModelNames(apiName)
	seen := make(map[string]struct{}, len(modelNames))
	newModelNames := make([]string, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := existing[modelName]; ok {
			continue
		}
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		newModelNames = append(newModelNames, modelName)
	}
	return newModelNames
}

func isHTTPURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// isOpenAICompatible reports whether the provider uses the standard
// OpenAI-compatible /chat/completions endpoint.
func isOpenAICompatible(apiName string) bool {
	return providerinfo.IsOpenAICompatible(apiName)
}

// testConnection makes a minimal chat completion request to verify the
// API key and endpoint work. Only meaningful for OpenAI-compatible providers.
func testConnection(model, baseURL, apiKey string) error {
	return testConnectionContext(context.Background(), model, baseURL, apiKey)
}

func testConnectionContext(ctx context.Context, model, baseURL, apiKey string) error {
	body := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 5,
		"stream":     false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400:
		return fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	default:
		return nil
	}
}

// discoverModels queries a provider's list-models endpoint and returns the
// available model IDs. Supports OpenAI-compatible, Anthropic, and Ollama
// protocols. Best-effort: many Anthropic-compatible gateways do not implement
// /v1/models, so callers must handle errors and fall back to manual entry.
func discoverModels(apiType, baseURL, apiKey string) ([]string, error) {
	return discoverModelsContext(context.Background(), apiType, baseURL, apiKey)
}

func discoverModelsContext(ctx context.Context, apiType, baseURL, apiKey string) ([]string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch apiType {
	case "ollama":
		return fetchModelIDsContext(ctx, baseURL+"/api/tags", "", "", nil)
	case "anthropic":
		root := anthropic.NormalizeBaseURL(baseURL)
		headers := map[string]string{"anthropic-version": "2023-06-01"}
		ids, err := fetchModelIDsContext(ctx, root+"/v1/models?limit=1000", "x-api-key", apiKey, headers)
		if err == nil {
			return ids, nil
		}
		// Many Anthropic-compatible gateways omit /v1/models but expose an
		// OpenAI-style /models list; try it with the same auth headers.
		ids2, err2 := fetchModelIDsContext(ctx, root+"/models?limit=1000", "x-api-key", apiKey, headers)
		if err2 == nil {
			return ids2, nil
		}
		return nil, fmt.Errorf("%w (also tried /models: %v)", err, err2)
	case "google":
		return fetchGoogleModelsContext(ctx, googleListModelsBase(baseURL)+"/models?key="+url.QueryEscape(apiKey))
	case "github-copilot":
		ids, _, err := discoverCopilotModelsContext(ctx, baseURL, apiKey)
		return ids, err
	default:
		// OpenAI-compatible: base URL typically ends in /v1; append /models.
		return fetchModelIDsContext(ctx, baseURL+"/models", "Authorization", "Bearer "+apiKey, nil)
	}
}

func discoverCopilotModels(baseURL, apiKey string) ([]string, map[string]string, error) {
	return discoverCopilotModelsContext(context.Background(), baseURL, apiKey)
}

func discoverCopilotModelsContext(ctx context.Context, baseURL, apiKey string) ([]string, map[string]string, error) {
	infos, err := copilot.DiscoverModelInfos(ctx, copilot.Client{
		APIBaseURL:     copilotGitHubAPIBaseURL,
		CopilotBaseURL: baseURL,
	}, apiKey)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(infos))
	endpoints := make(map[string]string, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
		endpoints[info.ID] = copilot.SelectEndpoint(info)
	}
	return ids, endpoints, nil
}

// fetchModelIDs performs a GET and extracts model identifiers from either an
// OpenAI/Anthropic-shaped response ({"data":[{"id":"..."}]}) or an
// Ollama-shaped one ({"models":[{"name":"..."}]}).
func fetchModelIDs(url, authHeader, authValue string, extraHeaders map[string]string) ([]string, error) {
	return fetchModelIDsContext(context.Background(), url, authHeader, authValue, extraHeaders)
}

func fetchModelIDsContext(ctx context.Context, url, authHeader, authValue string, extraHeaders map[string]string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil) //nolint:gosec,noctx
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if authHeader != "" && authValue != "" {
		req.Header.Set(authHeader, authValue)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ids := make([]string, 0)
	if raw, ok := body["data"]; ok {
		var arr []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &arr) == nil {
			for _, m := range arr {
				if m.ID != "" {
					ids = append(ids, m.ID)
				}
			}
		}
	}
	if len(ids) == 0 {
		if raw, ok := body["models"]; ok {
			var arr []struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &arr) == nil {
				for _, m := range arr {
					if m.Name != "" {
						ids = append(ids, m.Name)
					}
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no models returned")
	}
	sort.Strings(ids)
	return ids, nil
}

// googleListModelsBase normalizes a Google base URL to the API root used for
// listing models. The --config wizard's fallback (builtinBaseURL) returns the
// full streamGenerateContent endpoint with a {model} placeholder, and users
// may paste a similar URL. This function strips any /models/... suffix and
// query/fragment so discoverModels can append /models?key=... cleanly.
// An empty base returns the public default root.
func googleListModelsBase(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return "https://generativelanguage.googleapis.com/v1beta"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "https://generativelanguage.googleapis.com/v1beta"
	}
	path := u.Path
	if idx := strings.Index(path, "/models"); idx >= 0 {
		rest := path[idx+len("/models"):]
		if rest == "" || strings.HasPrefix(rest, "/") {
			path = path[:idx]
		}
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// fetchGoogleModels queries the Google Generative Language list-models endpoint
// and returns model IDs that support generateContent (filtering out embedding
// and text-only models). Auth is via the key= query parameter, not a header.
func fetchGoogleModels(urlStr string) ([]string, error) {
	return fetchGoogleModelsContext(context.Background(), urlStr)
}

func fetchGoogleModelsContext(ctx context.Context, urlStr string) ([]string, error) {
	ids, err := fetchGoogleModelsWithClientContext(ctx, urlStr, &http.Client{Timeout: 15 * time.Second})
	if err == nil || !isNetworkError(err) {
		return ids, err
	}

	// Some VPNs advertise an IPv6 route that fails immediately even though the
	// same endpoint is reachable over IPv4. Preserve normal dual-stack behavior
	// first, then retry transport failures over IPv4 before falling back to
	// manual model entry.
	ids, ipv4Err := fetchGoogleModelsWithClientContext(ctx, urlStr, newIPv4DiscoveryClient())
	if ipv4Err == nil {
		return ids, nil
	}
	return nil, fmt.Errorf("%v (IPv4 retry failed: %v)", err, ipv4Err)
}

func fetchGoogleModelsWithClient(urlStr string, client *http.Client) ([]string, error) {
	return fetchGoogleModelsWithClientContext(context.Background(), urlStr, client)
}

func fetchGoogleModelsWithClientContext(ctx context.Context, urlStr string, client *http.Client) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil) //nolint:gosec,noctx
	if err != nil {
		// urlStr contains the API key in its query string, so never include the
		// rejected URL in an error shown by the configuration UI.
		return nil, fmt.Errorf("build request: invalid model discovery URL")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, modelDiscoveryNetworkError(err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	}

	var body struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ids := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name == "" {
			continue
		}
		// Only keep models that support generateContent (chat/generation),
		// filtering out embedding, text-suffix, and other non-generative models.
		supportsGenerate := false
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				supportsGenerate = true
				break
			}
		}
		if !supportsGenerate {
			continue
		}
		ids = append(ids, name)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no generative models returned")
	}
	sort.Strings(ids)
	return ids, nil
}

func modelDiscoveryNetworkError(err error) error {
	// http.Client wraps transport failures in url.Error, whose Error method
	// includes the full request URL. Google's key is a query parameter, so
	// unwrap it before formatting to keep credentials out of the terminal.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return fmt.Errorf("network error: %w", err)
}

func isNetworkError(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func newIPv4DiscoveryClient() *http.Client {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

// resolveKeyForDiscovery returns a usable API key for model discovery: the key
// just entered in the wizard if present, otherwise the provider's configured
// key (api-key or its env var). Returns "" if none is available.
func resolveKeyForDiscovery(apiName, enteredKey string) string {
	if k := strings.TrimSpace(enteredKey); k != "" {
		return k
	}
	if k := configuredAPIKey(apiName); k != "" {
		return k
	}
	// Use the same resolved environment-variable name shown on the preceding
	// credentials page. During first-time setup that name has not been saved to
	// the YAML yet, so consulting only api.APIKeyEnv would miss keys such as the
	// built-in GOOGLE_API_KEY and make discovery send an empty credential.
	return os.Getenv(resolveEnvVar(apiName))
}

func configuredAPIKey(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			return api.APIKey
		}
	}
	return ""
}

func boolLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}
