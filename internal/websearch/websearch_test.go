package websearch

import (
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

func TestParseBingBlock(t *testing.T) {
	t.Run("complete block", func(t *testing.T) {
		html := `<li class="b_algo"><h2><a href="https://example.com">Example Title</a></h2><div class="b_caption"><p>Example snippet text</p></div></li>`
		result := parseBingBlock(html)
		require.Equal(t, "https://example.com", result.URL)
		require.Equal(t, "Example Title", result.Title)
		require.Equal(t, "Example snippet text", result.Snippet)
	})

	t.Run("no h2 returns empty", func(t *testing.T) {
		result := parseBingBlock(`<li class="b_algo"><div>no h2</div></li>`)
		require.Empty(t, result.URL)
		require.Empty(t, result.Title)
	})

	t.Run("no caption", func(t *testing.T) {
		html := `<li class="b_algo"><h2><a href="https://x.com">Title</a></h2></li>`
		result := parseBingBlock(html)
		require.Equal(t, "https://x.com", result.URL)
		require.Equal(t, "Title", result.Title)
		require.Empty(t, result.Snippet)
	})
}

func TestParseBingHTML(t *testing.T) {
	t.Run("single result", func(t *testing.T) {
		html := `<li class="b_algo"><h2><a href="https://a.com">A Title</a></h2><div class="b_caption"><p>Snippet</p></div></li>`
		results := parseBingHTML(html, 3)
		require.Len(t, results, 1)
		require.Equal(t, "A Title", results[0].Title)
	})

	t.Run("respects max results", func(t *testing.T) {
		html := `<li class="b_algo"><h2><a href="https://1.com">One</a></h2></li>`
		html += `<li class="b_algo"><h2><a href="https://2.com">Two</a></h2></li>`
		html += `<li class="b_algo"><h2><a href="https://3.com">Three</a></h2></li>`
		results := parseBingHTML(html, 2)
		require.Len(t, results, 2)
	})

	t.Run("no results", func(t *testing.T) {
		results := parseBingHTML("<div>no algo</div>", 5)
		require.Empty(t, results)
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

func TestParseGoogleBlock(t *testing.T) {
	t.Run("complete block", func(t *testing.T) {
		html := `<div class="g"><a href="https://example.com"><h3>Example Title</h3></a><span class="VwiC3b">Example snippet text</span></div>`
		result := parseGoogleBlock(html)
		require.Equal(t, "https://example.com", result.URL)
		require.Equal(t, "Example Title", result.Title)
		require.Contains(t, result.Snippet, "Example snippet text")
	})

	t.Run("no h3 returns empty", func(t *testing.T) {
		result := parseGoogleBlock(`<div class="g"><a href="https://x.com">no title</a></div>`)
		require.Empty(t, result.Title)
	})

	t.Run("h3 without anchor", func(t *testing.T) {
		result := parseGoogleBlock(`<div class="g"><h3>Title</h3><span>Snippet</span></div>`)
		require.Equal(t, "Title", result.Title)
	})
}

func TestParseGoogleHTML(t *testing.T) {
	t.Run("single result", func(t *testing.T) {
		html := `<div class="g"><a href="https://a.com"><h3>A Title</h3></a><span class="VwiC3b">Snippet here</span></div>`
		results := parseGoogleHTML(html, 3)
		require.Len(t, results, 1)
		require.Equal(t, "A Title", results[0].Title)
	})

	t.Run("no results", func(t *testing.T) {
		results := parseGoogleHTML("<div>no results</div>", 5)
		require.Empty(t, results)
	})
}

