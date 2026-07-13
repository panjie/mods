package cli

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/panjie/mods/internal/skills"
)

var scanSkills = skills.ScanDirs
var renderSkillsMarkdown = func(mods *Mods, content string) (string, error) {
	return mods.RenderMarkdown(content)
}

// listSkills prints the installed skills discovered in dirs. Missing or
// empty directories are a valid empty catalog; actual scan failures are errors.
func listSkills(mods *Mods, dirs []string) error {
	catalog, err := scanSkills(dirs)
	if err != nil {
		return modsError{
			Err:        err,
			ReasonText: "Could not scan skills directories.",
		}
	}
	markdown := skillsMarkdown(dirs, catalog)
	if !IsOutputTTY() || config.Raw {
		_, _ = lipgloss.Fprint(os.Stdout, markdown)
		return nil
	}

	rendered, err := renderSkillsMarkdown(mods, markdown)
	if err != nil {
		return modsError{
			Err:        err,
			ReasonText: "Could not render skills list.",
		}
	}
	_, _ = lipgloss.Fprintln(os.Stdout, strings.TrimRightFunc(rendered, unicode.IsSpace))
	return nil
}

func skillsMarkdown(dirs []string, catalog []skills.Skill) string {
	var sb strings.Builder
	sb.WriteString("# Skills\n\n")
	if len(dirs) == 1 {
		sb.WriteString("Directory: ")
		sb.WriteString(markdownInlineCode(dirs[0]))
	} else {
		sb.WriteString("Directories:")
		for _, dir := range dirs {
			sb.WriteString("\n- ")
			sb.WriteString(markdownInlineCode(dir))
		}
	}
	sb.WriteString("\n\n")
	if len(catalog) == 0 {
		sb.WriteString("_No skills found._\n")
		return sb.String()
	}

	for _, skill := range catalog {
		sb.WriteString("- **")
		sb.WriteString(escapeMarkdownText(skill.Name))
		sb.WriteString("** — ")
		sb.WriteString(escapeMarkdownText(firstSentence(skill.Description)))
		sb.WriteString("\n")
	}
	sb.WriteString("\n_")
	sb.WriteString(fmt.Sprintf("%d ", len(catalog)))
	label := "skills"
	if len(catalog) == 1 {
		label = "skill"
	}
	sb.WriteString(label)
	sb.WriteString("_\n")
	return sb.String()
}

func escapeMarkdownText(text string) string {
	var sb strings.Builder
	for _, r := range text {
		if strings.ContainsRune(`\`+"`*_[]<>", r) {
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func markdownInlineCode(text string) string {
	longest := 0
	current := 0
	for _, r := range text {
		if r == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") ||
		strings.HasPrefix(text, " ") || strings.HasSuffix(text, " ") {
		return fence + " " + text + " " + fence
	}
	return fence + text + fence
}

// firstSentence returns the first sentence of a skill description. Sentence
// punctuation inside extensions such as .docx is not followed by whitespace,
// and common abbreviations such as e.g. are explicitly ignored.
func firstSentence(description string) string {
	description = strings.TrimSpace(description)
	for i, r := range description {
		switch r {
		case '\n', '\r':
			return strings.TrimSpace(description[:i])
		case '。', '！', '？':
			return strings.TrimSpace(description[:i+utf8.RuneLen(r)])
		case '.', '!', '?':
			end := i + utf8.RuneLen(r)
			for end < len(description) {
				next, size := utf8.DecodeRuneInString(description[end:])
				if !strings.ContainsRune(`"'”’)]}`, next) {
					break
				}
				end += size
			}
			if end < len(description) {
				next, _ := utf8.DecodeRuneInString(description[end:])
				if !unicode.IsSpace(next) {
					continue
				}
			}
			if r == '.' && isSentenceAbbreviation(description[:i+1]) {
				continue
			}
			return strings.TrimSpace(description[:end])
		}
	}
	return description
}

func isSentenceAbbreviation(prefix string) bool {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	word := strings.ToLower(strings.Trim(fields[len(fields)-1], `"'“‘([{`))
	switch word {
	case "e.g.", "i.e.", "etc.", "mr.", "mrs.", "ms.", "dr.", "prof.", "vs.":
		return true
	default:
		return false
	}
}
