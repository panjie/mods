package cli

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/panjie/mods/internal/skills"
)

var scanSkills = skills.ScanDirs

// listSkills prints the installed skills discovered in dirs. Missing or
// empty directories are a valid empty catalog; actual scan failures are errors.
func listSkills(dirs []string) error {
	catalog, err := scanSkills(dirs)
	if err != nil {
		return modsError{
			Err:        err,
			ReasonText: "Could not scan skills directories.",
		}
	}
	slices.SortFunc(catalog, func(a, b skills.Skill) int {
		return strings.Compare(a.Name, b.Name)
	})
	rows := make([][]string, 0, len(catalog))
	for _, skill := range catalog {
		rows = append(rows, []string{skill.Name, firstSentence(normalizeListDescription(skill.Description))})
	}
	printListView(listView{
		Title: "Skills",
		Meta:  skillsDirectoryMeta(dirs),
		Columns: []listColumn{
			{Header: "NAME"},
			{Header: "DESCRIPTION", Flexible: true},
		},
		Rows:    rows,
		Empty:   "No skills found.",
		Summary: listCount(len(rows), "skill", "skills"),
	})
	return nil
}

func skillsDirectoryMeta(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	if len(dirs) == 1 {
		return []string{"Directory  " + dirs[0]}
	}
	meta := []string{"Directories"}
	for _, dir := range dirs {
		meta = append(meta, "  "+dir)
	}
	return meta
}

func normalizeListDescription(description string) string {
	return strings.Join(strings.Fields(description), " ")
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
