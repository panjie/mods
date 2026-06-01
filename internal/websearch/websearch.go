package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	defaultMaxResults = 5
	SearchTimeout     = 10 * time.Second
)

type Config struct {
	Enabled    bool
	Provider   string
	APIKey     string
	BaseURL    string
	MaxResults int
}

func Tool() mcp.Tool {
	return mcp.Tool{
		Name:        "web_search",
		Description: "Search the web for current, up-to-date information. Use this tool when you need information that may not be in your training data, or when you need the latest news, facts, or real-time data. Returns formatted search results with titles, URLs, and snippets.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The search query to look up on the web",
				},
			},
			Required: []string{"query"},
		},
	}
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
	provider := cfg.Provider
	if provider == "" {
		provider = "bing"
	}
	maxResults := cfg.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}

	switch provider {
	case "tavily":
		if cfg.APIKey == "" {
			return nil, fmt.Errorf("web search: tavily provider requires an API key")
		}
		return searchTavily(ctx, cfg.APIKey, query, maxResults)
	case "bing":
		return searchBing(ctx, query, maxResults)
	case "custom":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("web search: custom provider requires a base URL")
		}
		return searchCustom(ctx, cfg.BaseURL, cfg.APIKey, query, maxResults)
	default:
		return searchBing(ctx, query, maxResults)
	}
}

func httpClient() *http.Client {
	return &http.Client{Timeout: SearchTimeout}
}

func newRequest(ctx context.Context, method, urlStr string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, urlStr, nil)
	if err != nil {
		return nil, err
	}
	ua := "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	return req, nil
}

func searchBing(ctx context.Context, query string, maxResults int) ([]Result, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("setlang", "en")
	params.Set("count", fmt.Sprintf("%d", maxResults))

	u := "https://www.bing.com/search?" + params.Encode()
	req, err := newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, fmt.Errorf("searching Bing: %w", err)
	}

	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching Bing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searching Bing: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("searching Bing: %w", err)
	}

	return parseBingHTML(string(body), maxResults), nil
}

func parseBingHTML(html string, maxResults int) []Result {
	var results []Result

	for len(results) < maxResults {
		liIdx := strings.Index(html, `class="b_algo"`)
		if liIdx < 0 {
			break
		}
		html = html[liIdx:]

		liEnd := strings.Index(html, `</li>`)
		if liEnd < 0 {
			break
		}
		block := html[:liEnd+5]
		html = html[liEnd+5:]

		result := parseBingBlock(block)
		if result.Title == "" && result.Snippet == "" {
			continue
		}
		results = append(results, result)
	}

	return results
}

func parseBingBlock(block string) Result {
	var result Result

	h2Start := strings.Index(block, "<h2")
	if h2Start < 0 {
		return result
	}
	h2Block := block[h2Start:]

	hrefStart := strings.Index(h2Block, `href="`)
	if hrefStart >= 0 {
		hrefStart += 6
		hrefEnd := strings.Index(h2Block[hrefStart:], `"`)
		if hrefEnd >= 0 {
			result.URL = h2Block[hrefStart : hrefStart+hrefEnd]
		}
	}

	aStart := strings.Index(h2Block, ">")
	aEnd := strings.Index(h2Block, "</a>")
	if aStart >= 0 && aEnd > aStart {
		inner := h2Block[aStart+1 : aEnd]
		result.Title = cleanHTML(inner)
	}

	captionStart := strings.Index(block, `class="b_caption"`)
	if captionStart >= 0 {
		captionBlock := block[captionStart:]
		pStart := strings.Index(captionBlock, "<p")
		if pStart >= 0 {
			pBlock := captionBlock[pStart:]
			pTagEnd := strings.Index(pBlock, ">")
			pClose := strings.Index(pBlock, "</p>")
			if pTagEnd >= 0 && pClose > pTagEnd {
				inner := pBlock[pTagEnd+1 : pClose]
				result.Snippet = cleanHTML(inner)
			}
		}
	}

	return result
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
