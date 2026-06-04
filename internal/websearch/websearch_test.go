package websearch

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanHTML(t *testing.T) {
	t.Run("removes tags", func(t *testing.T) {
		require.Equal(t, "Hello World", cleanHTML("<p>Hello <b>World</b></p>"))
	})
	t.Run("decodes entities", func(t *testing.T) {
		require.Equal(t, "A & B < C", cleanHTML("A &amp; B &lt; C"))
	})
	t.Run("handles quotes", func(t *testing.T) {
		require.Equal(t, `"hello" 'world'`, cleanHTML("&quot;hello&quot; &#39;world&#39;"))
	})
	t.Run("collapse whitespace", func(t *testing.T) {
		require.Equal(t, "a b c", cleanHTML("  a   b   c  "))
	})
	t.Run("empty", func(t *testing.T) {
		require.Empty(t, cleanHTML(""))
	})
}

func TestSearchResultFormat(t *testing.T) {
	results := []Result{
		{Title: "T1", URL: "https://u1.com", Snippet: "S1"},
		{Title: "T2", URL: "https://u2.com", Snippet: "S2"},
	}
	output := formatResults("test query", results)
	require.Contains(t, output, "test query")
	require.Contains(t, output, "1. T1")
	require.Contains(t, output, "2. T2")
	require.Contains(t, output, "https://u1.com")
	require.Contains(t, output, "S1")
}

func formatResults(query string, results []Result) string {
	var sb []byte
	sb = append(sb, fmt.Sprintf("Web search results for \"%s\":\n\n", query)...)
	for i, r := range results {
		sb = append(sb, fmt.Sprintf("%d. %s\n   URL: %s\n   %s\n\n",
			i+1, r.Title, r.URL, r.Snippet)...)
	}
	return string(sb)
}

func TestParseDuckDuckGoInstant(t *testing.T) {
	t.Run("answer", func(t *testing.T) {
		results := parseDuckDuckGoInstant("life", duckDuckGoInstantResponse{
			Answer:     "42",
			AnswerType: "answer",
		}, 5)
		require.Len(t, results, 1)
		require.Equal(t, "answer", results[0].Title)
		require.Equal(t, "https://duckduckgo.com/?q=life", results[0].URL)
		require.Equal(t, "42", results[0].Snippet)
	})

	t.Run("abstract", func(t *testing.T) {
		results := parseDuckDuckGoInstant("go", duckDuckGoInstantResponse{
			Heading:      "Go",
			AbstractText: "Go is a programming language.",
			AbstractURL:  "https://example.com/go",
		}, 5)
		require.Len(t, results, 1)
		require.Equal(t, "Go", results[0].Title)
		require.Equal(t, "https://example.com/go", results[0].URL)
		require.Equal(t, "Go is a programming language.", results[0].Snippet)
	})

	t.Run("definition", func(t *testing.T) {
		results := parseDuckDuckGoInstant("term", duckDuckGoInstantResponse{
			Heading:       "Term",
			Definition:    "A word or phrase.",
			DefinitionURL: "https://example.com/term",
		}, 5)
		require.Len(t, results, 1)
		require.Equal(t, "Term", results[0].Title)
		require.Equal(t, "https://example.com/term", results[0].URL)
		require.Equal(t, "A word or phrase.", results[0].Snippet)
	})

	t.Run("nested related topics", func(t *testing.T) {
		results := parseDuckDuckGoInstant("mods", duckDuckGoInstantResponse{
			RelatedTopics: []duckDuckGoTopic{
				{
					Topics: []duckDuckGoTopic{
						{
							FirstURL: "https://example.com/mods",
							Text:     "mods - AI on the command line",
						},
					},
				},
			},
		}, 5)
		require.Len(t, results, 1)
		require.Equal(t, "mods", results[0].Title)
		require.Equal(t, "https://example.com/mods", results[0].URL)
		require.Equal(t, "mods - AI on the command line", results[0].Snippet)
	})

	t.Run("respects max results", func(t *testing.T) {
		results := parseDuckDuckGoInstant("x", duckDuckGoInstantResponse{
			Answer:       "answer",
			AbstractText: "abstract",
			Definition:   "definition",
		}, 2)
		require.Len(t, results, 2)
	})

	t.Run("empty response", func(t *testing.T) {
		require.Empty(t, parseDuckDuckGoInstant("empty", duckDuckGoInstantResponse{}, 5))
	})
}

func TestNormalizeProvider(t *testing.T) {
	tests := map[string]provider{
		"":           providerDuckDuckGo,
		"duckduckgo": providerDuckDuckGo,
		"ddg":        providerDuckDuckGo,
		"google":     providerDuckDuckGo,
		"bing":       providerDuckDuckGo,
		"tavily":     providerTavily,
		"custom":     providerCustom,
		"https://x":  providerCustom,
		"unknown":    providerDuckDuckGo,
	}
	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, normalizeProvider(input))
		})
	}
}

func TestSearchProviderValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("tavily requires api key", func(t *testing.T) {
		_, err := search(ctx, Config{Provider: "tavily"}, "query")
		require.EqualError(t, err, "web search: tavily provider requires an API key")
	})

	t.Run("custom requires base url", func(t *testing.T) {
		_, err := search(ctx, Config{Provider: "custom"}, "query")
		require.EqualError(t, err, "web search: custom provider requires a base URL")
	})
}
