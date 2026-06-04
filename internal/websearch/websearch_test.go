package websearch

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTool(t *testing.T) {
	tool := Tool()
	require.Equal(t, "web_search", tool.Name)
	require.NotEmpty(t, tool.Description)
	require.Equal(t, "object", tool.InputSchema.Type)
	props, ok := tool.InputSchema.Properties["query"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", props["type"])
	require.Contains(t, tool.InputSchema.Required, "query")
}

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

