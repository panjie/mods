package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/panjie/mods/internal/textutil"
)

// searchDeepSeek implements https://api-docs.deepseek.com/api/search/.
// The summary field is intended for model consumption; full page content is
// unnecessary for the shared search result format.
func searchDeepSeek(ctx context.Context, client *http.Client, apiKey, query string, maxResults int) ([]Result, error) {
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 128 {
		return nil, fmt.Errorf("searching DeepSeek: query must contain 1 to 128 Unicode characters and cannot be all whitespace")
	}
	if maxResults < 1 || maxResults > 50 {
		return nil, fmt.Errorf("searching DeepSeek: result count must be between 1 and 50")
	}

	body, err := json.Marshal(struct {
		Query       string `json:"query"`
		Count       int    `json:"count"`
		NeedContent bool   `json:"need_content"`
	}{Query: query, Count: maxResults})
	if err != nil {
		return nil, fmt.Errorf("searching DeepSeek: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.deepseek.com/v1/tools/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("searching DeepSeek: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searching DeepSeek: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("searching DeepSeek: HTTP %d: %s", resp.StatusCode,
			textutil.TruncateUTF8Bytes(string(body), 200))
	}
	var response struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Summary string `json:"summary"`
			Snippet string `json:"snippet"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("searching DeepSeek: %w", err)
	}
	results := make([]Result, 0, len(response.Results))
	for _, item := range response.Results {
		snippet := item.Summary
		if strings.TrimSpace(snippet) == "" {
			snippet = item.Snippet
		}
		results = append(results, Result{Title: item.Title, URL: item.URL, Snippet: snippet})
	}
	return results, nil
}
