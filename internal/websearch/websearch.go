package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	defaultMaxResults = 5
	SearchTimeout     = 10 * time.Second
)

type provider string

const (
	providerDuckDuckGo provider = "duckduckgo"
	providerTavily     provider = "tavily"
	providerCustom     provider = "custom"
)

type Config struct {
	Enabled    bool
	Provider   string
	APIKey     string
	BaseURL    string
	MaxResults int
}

func Search(ctx context.Context, cfg Config, query string) (string, error) {
	results, err := search(ctx, cfg, query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", fmt.Errorf("web search: no results found for %q", query)
	}

	var sb strings.Builder
	sb.WriteString("Web search results for \"" + query + "\":\n\n")
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n   URL: %s\n   %s\n\n",
			i+1, r.Title, r.URL, r.Snippet))
	}
	return sb.String(), nil
}

type Result struct {
	Title   string
	URL     string
	Snippet string
}

func search(ctx context.Context, cfg Config, query string) ([]Result, error) {
	provider := normalizeProvider(cfg.Provider)
	baseURL := cfg.BaseURL
	if providerURL := providerBaseURL(cfg.Provider); providerURL != "" {
		provider = providerCustom
		baseURL = providerURL
	}
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	switch provider {
	case providerDuckDuckGo:
		return searchDuckDuckGoInstant(ctx, query, maxResults)
	case providerTavily:
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("web search: tavily provider requires an API key")
		}
		return searchTavily(ctx, cfg.APIKey, query, maxResults)
	case providerCustom:
		if baseURL == "" {
			return nil, fmt.Errorf("web search: custom provider requires a base URL")
		}
		return searchCustom(ctx, baseURL, cfg.APIKey, query, maxResults)
	default:
		return searchDuckDuckGoInstant(ctx, query, maxResults)
	}
}

func normalizeProvider(value string) provider {
	value = strings.ToLower(strings.TrimSpace(value))
	if providerBaseURL(value) != "" {
		return providerCustom
	}
	switch value {
	case "", "duckduckgo", "ddg", "google", "bing":
		return providerDuckDuckGo
	case "tavily":
		return providerTavily
	case "custom":
		return providerCustom
	default:
		return providerDuckDuckGo
	}
}

func providerBaseURL(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value
	}
	return ""
}

func httpClient() *http.Client {
	return &http.Client{
		Timeout:   SearchTimeout,
		Transport: safeSearchTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if privateSearchAllowed() {
				return nil
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return validateProviderURL(req.URL.String())
		},
	}
}

// privateSearchAllowed reports whether the user has explicitly opted in to
// allowing web search providers that resolve to private or loopback addresses
// (useful for local development against a self-hosted search API). Read on
// every call so tests and long-lived processes pick up env changes.
func privateSearchAllowed() bool {
	return os.Getenv("MODS_WEB_SEARCH_ALLOW_PRIVATE") == "1"
}

// isBlockedAddress reports whether an IP is internal/loopback/link-local,
// i.e. must never be reachable from a user-controlled search provider URL.
func isBlockedAddress(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// validateProviderURL rejects URLs whose host is a private/loopback IP literal.
// Hostnames are checked again at dial time (see safeDialContext) to defeat
// DNS rebinding between validation and connect.
func validateProviderURL(rawURL string) error {
	if privateSearchAllowed() {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("web search: invalid provider URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("web search: provider URL is missing a host")
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedAddress(ip) {
		return fmt.Errorf("web search: provider URL host %s is a private or loopback address; set MODS_WEB_SEARCH_ALLOW_PRIVATE=1 to allow", host)
	}
	return nil
}

// safeSearchTransport returns an http.Transport whose DialContext refuses to
// connect to any resolved private/loopback/link-local address. This is the
// authoritative SSRF defence: even if a hostname resolves to a public IP at
// validation time and to 127.0.0.1 a millisecond later (DNS rebinding), the
// dial is refused.
func safeSearchTransport() *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if privateSearchAllowed() {
				return dialer.DialContext(ctx, network, addr)
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, resolved := range ips {
				if isBlockedAddress(resolved.IP) {
					return nil, fmt.Errorf("web search: refused to dial private or loopback address %s for %q", resolved.IP, host)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}

func newRequest(ctx context.Context, method, urlStr string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return nil, err
	}
	ua := fmt.Sprintf("Mozilla/5.0 (%s; %s) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36", runtime.GOOS, runtime.GOARCH)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	return req, nil
}

func searchDuckDuckGoInstant(ctx context.Context, query string, maxResults int) ([]Result, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("no_redirect", "1")
	params.Set("no_html", "1")
	params.Set("skip_disambig", "1")

	u := "https://api.duckduckgo.com/?" + params.Encode()
	req, err := newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, fmt.Errorf("searching DuckDuckGo: %w", err)
	}

	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching DuckDuckGo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searching DuckDuckGo: HTTP %d", resp.StatusCode)
	}

	var ddgResp duckDuckGoInstantResponse
	if err := json.NewDecoder(resp.Body).Decode(&ddgResp); err != nil {
		return nil, fmt.Errorf("searching DuckDuckGo: %w", err)
	}

	return parseDuckDuckGoInstant(query, ddgResp, maxResults), nil
}

type duckDuckGoInstantResponse struct {
	Answer        string            `json:"Answer"`
	AnswerType    string            `json:"AnswerType"`
	AbstractText  string            `json:"AbstractText"`
	AbstractURL   string            `json:"AbstractURL"`
	Definition    string            `json:"Definition"`
	DefinitionURL string            `json:"DefinitionURL"`
	Heading       string            `json:"Heading"`
	RelatedTopics []duckDuckGoTopic `json:"RelatedTopics"`
}

type duckDuckGoTopic struct {
	FirstURL string            `json:"FirstURL"`
	Text     string            `json:"Text"`
	Topics   []duckDuckGoTopic `json:"Topics"`
}

func parseDuckDuckGoInstant(query string, resp duckDuckGoInstantResponse, maxResults int) []Result {
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	results := make([]Result, 0, maxResults)
	fallbackURL := duckDuckGoFallbackURL(query)
	title := strings.TrimSpace(resp.Heading)
	if title == "" {
		title = query
	}

	if answer := strings.TrimSpace(resp.Answer); answer != "" {
		answerTitle := title
		if resp.AnswerType != "" {
			answerTitle = strings.TrimSpace(resp.AnswerType)
		}
		results = appendDuckDuckGoResult(results, maxResults, Result{
			Title:   answerTitle,
			URL:     fallbackURL,
			Snippet: answer,
		})
	}

	if abstract := strings.TrimSpace(resp.AbstractText); abstract != "" {
		results = appendDuckDuckGoResult(results, maxResults, Result{
			Title:   title,
			URL:     firstNonEmpty(resp.AbstractURL, fallbackURL),
			Snippet: abstract,
		})
	}

	if definition := strings.TrimSpace(resp.Definition); definition != "" {
		results = appendDuckDuckGoResult(results, maxResults, Result{
			Title:   title,
			URL:     firstNonEmpty(resp.DefinitionURL, fallbackURL),
			Snippet: definition,
		})
	}

	return appendDuckDuckGoTopics(results, maxResults, resp.RelatedTopics, fallbackURL)
}

func appendDuckDuckGoTopics(results []Result, maxResults int, topics []duckDuckGoTopic, fallbackURL string) []Result {
	for _, topic := range topics {
		if len(results) >= maxResults {
			break
		}
		if len(topic.Topics) > 0 {
			results = appendDuckDuckGoTopics(results, maxResults, topic.Topics, fallbackURL)
			continue
		}
		text := strings.TrimSpace(topic.Text)
		if text == "" {
			continue
		}
		title := text
		if first, _, ok := strings.Cut(text, " - "); ok {
			title = first
		}
		results = appendDuckDuckGoResult(results, maxResults, Result{
			Title:   title,
			URL:     firstNonEmpty(topic.FirstURL, fallbackURL),
			Snippet: text,
		})
	}
	return results
}

func appendDuckDuckGoResult(results []Result, maxResults int, result Result) []Result {
	if len(results) >= maxResults || strings.TrimSpace(result.Snippet) == "" {
		return results
	}
	result.Title = cleanHTML(result.Title)
	result.URL = strings.TrimSpace(result.URL)
	result.Snippet = cleanHTML(result.Snippet)
	return append(results, result)
}

func duckDuckGoFallbackURL(query string) string {
	params := url.Values{}
	params.Set("q", query)
	return "https://duckduckgo.com/?" + params.Encode()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func searchTavily(ctx context.Context, apiKey, query string, maxResults int) ([]Result, error) {
	body := map[string]any{
		"api_key":      apiKey,
		"query":        query,
		"search_depth": "basic",
		"max_results":  maxResults,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("searching Tavily: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		"https://api.tavily.com/search",
		bytes.NewReader(bodyJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("searching Tavily: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching Tavily: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("searching Tavily: HTTP %d: %s", resp.StatusCode,
			string(respBody[:min(len(respBody), 200)]))
	}

	var tavilyResp struct {
		Results []struct {
			Title   string  `json:"title"`
			URL     string  `json:"url"`
			Content string  `json:"content"`
			Score   float64 `json:"score"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("searching Tavily: %w", err)
	}

	var results []Result
	for _, r := range tavilyResp.Results {
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
		})
	}

	return results, nil
}

func searchCustom(ctx context.Context, baseURL, apiKey, query string, maxResults int) ([]Result, error) {
	if err := validateProviderURL(baseURL); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", fmt.Sprintf("%d", maxResults))
	if apiKey != "" {
		params.Set("api_key", apiKey)
	}

	u := strings.TrimRight(baseURL, "/") + "/search?" + params.Encode()
	req, err := newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, fmt.Errorf("web search: %w", err)
	}

	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("web search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("web search: HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("web search: %w", err)
	}

	var customResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}

	if err := json.Unmarshal(body, &customResp); err != nil {
		return nil, fmt.Errorf("web search: %w", err)
	}

	var results []Result
	for _, r := range customResp.Results {
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Snippet,
		})
	}

	return results, nil
}

func cleanHTML(s string) string {
	s = strings.TrimSpace(s)
	for {
		tagStart := strings.Index(s, "<")
		if tagStart < 0 {
			break
		}
		tagEnd := strings.Index(s[tagStart:], ">")
		if tagEnd < 0 {
			break
		}
		s = s[:tagStart] + " " + s[tagStart+tagEnd+1:]
	}
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&ensp;", " ")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	words := strings.Fields(s)
	return strings.Join(words, " ")
}
