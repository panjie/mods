package cli

import (
	"math/rand"
	"regexp"
)

var examples = map[string]string{
	"Summarize piped JSON":             `printf '%s\n' '[{"name":"bubbletea"},{"name":"lipgloss"},{"name":"gum"}]' | mods -f "summarize these projects"`,
	"Return pipeline-friendly output":  `find . -maxdepth 1 -type f | sort | mods --minimal "pick the five most important files"`,
	"Format as JSON":                   `git log --oneline -5 | mods --format --format-as json "convert these commits to objects with sha and title"`,
	"Choose a configured model":        `mods --ask-model "Explain this project in one paragraph"`,
	"Use a specific API and model":     `mods --api openai --model gpt-5.4 "Draft a concise release note"`,
	"Search the web":                   `mods --web-search "What changed in the latest Go release?"`,
	"Describe an image":                `mods --image assets/mods-product.png "Describe this image and suggest alt text"`,
	"Describe an image from stdin":     `cat assets/mods-product.png | mods --stdin-image "Describe this image"`,
	"Describe an image from clipboard": `mods -I "Describe the image on my clipboard"`,
	"Save a conversation":              `mods --title project-summary "Summarize this repository"`,
	"Continue a conversation":          `mods --continue project-summary "Turn that summary into release notes"`,
	"Show recent conversations":        `mods --list`,
	"Use a custom role":                `mods --role shell "list the largest files in the current directory"`,
	"Review file edits":                `mods --review mutable --workspace . "Read README.md and write docs/cli-notes.md with a short usage guide"`,
	"Plan before acting":               `mods --plan --workspace . "Refactor the CLI examples to cover more features"`,
	"Inspect MCP servers":              `mods --mcp-list`,
	"Inspect MCP tools":                `mods --mcp-list-tools`,
	"Debug reasoning":                  `mods --reasoning auto --debug "When should I use each review mode?"`,
}

func randomExample() string {
	keys := make([]string, 0, len(examples))
	for k := range examples {
		keys = append(keys, k)
	}
	desc := keys[rand.Intn(len(keys))] //nolint:gosec
	return desc
}

func cheapHighlighting(s Styles, code string) string {
	code = regexp.
		MustCompile(`"([^"\\]|\\.)*"`).
		ReplaceAllStringFunc(code, func(x string) string {
			return s.Quote.Render(x)
		})
	code = regexp.
		MustCompile(`\|`).
		ReplaceAllStringFunc(code, func(x string) string {
			return s.Pipe.Render(x)
		})
	return code
}
