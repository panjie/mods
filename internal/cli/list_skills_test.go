package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestListSkillsOutputsSortedCatalogAndSummary(t *testing.T) {
	withListSkillsOutputTest(t, false, false)
	dir := t.TempDir()
	writeCLITestSkill(t, dir, "zeta", "Zeta skill. More detail that should be hidden.")
	writeCLITestSkill(t, dir, "alpha", "Alpha skill. More detail that should be hidden.")

	output := captureStdout(t, func() {
		require.NoError(t, listSkills(nil, dir))
	})

	require.Equal(t, "# Skills\n\nDirectory: `"+dir+"`\n\n- **alpha** — Alpha skill.\n- **zeta** — Zeta skill.\n\n_2 skills_\n", output)
}

func TestFirstSentenceHandlesExtensionsAbbreviationsAndChinese(t *testing.T) {
	require.Equal(t,
		"Use this skill for Word documents (.docx files).",
		firstSentence("Use this skill for Word documents (.docx files). Triggers include editing."),
	)
	require.Equal(t,
		"Use common formats, e.g. PDF and DOCX files.",
		firstSentence("Use common formats, e.g. PDF and DOCX files. More details."),
	)
	require.Equal(t, "用于处理文档。", firstSentence("用于处理文档。还可以编辑文档。"))
}

func TestListSkillsEmptyCatalogSucceeds(t *testing.T) {
	withListSkillsOutputTest(t, false, false)
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		output := captureStdout(t, func() {
			require.NoError(t, listSkills(nil, dir))
		})
		require.Equal(t, "# Skills\n\nDirectory: `"+dir+"`\n\n_No skills found._\n", output)
	}
}

func TestListSkillsScanFailureReturnsError(t *testing.T) {
	saved := scanSkills
	scanSkills = func(string) ([]skills.Skill, error) {
		return nil, errors.New("permission denied")
	}
	t.Cleanup(func() { scanSkills = saved })

	err := listSkills(nil, "/unreadable")

	require.Error(t, err)
	merr, ok := err.(modsError)
	require.True(t, ok)
	require.Equal(t, "Could not scan skills directory.", merr.ReasonText)
	require.ErrorContains(t, merr.Err, "permission denied")
}

func TestListSkillsRendersMarkdownForTTY(t *testing.T) {
	withListSkillsOutputTest(t, true, false)
	dir := t.TempDir()
	writeCLITestSkill(t, dir, "alpha", "Alpha skill. More details.")
	renderSkillsMarkdown = func(_ *Mods, content string) (string, error) {
		require.Contains(t, content, "- **alpha** — Alpha skill.")
		return "rendered skills\n\n", nil
	}

	output := captureStdout(t, func() {
		require.NoError(t, listSkills(&Mods{}, dir))
	})

	require.Equal(t, "rendered skills\n", output)
}

func TestListSkillsRawSkipsMarkdownRenderer(t *testing.T) {
	withListSkillsOutputTest(t, true, true)
	dir := t.TempDir()
	writeCLITestSkill(t, dir, "alpha", "Alpha skill.")
	renderSkillsMarkdown = func(_ *Mods, _ string) (string, error) {
		t.Fatal("renderer must not be called in raw mode")
		return "", nil
	}

	output := captureStdout(t, func() {
		require.NoError(t, listSkills(&Mods{}, dir))
	})
	require.Contains(t, output, "# Skills\n")
}

func TestListSkillsRenderFailureReturnsError(t *testing.T) {
	withListSkillsOutputTest(t, true, false)
	dir := t.TempDir()
	writeCLITestSkill(t, dir, "alpha", "Alpha skill.")
	renderSkillsMarkdown = func(_ *Mods, _ string) (string, error) {
		return "", errors.New("render failed")
	}

	err := listSkills(&Mods{}, dir)

	require.Error(t, err)
	merr, ok := err.(modsError)
	require.True(t, ok)
	require.Equal(t, "Could not render skills list.", merr.ReasonText)
}

func TestSkillsMarkdownEscapesContent(t *testing.T) {
	got := skillsMarkdown("/tmp/a`b", []skills.Skill{{
		Name:        "a*b",
		Description: "Use [docs] and <files>. More details.",
	}})

	require.Contains(t, got, "Directory: ``/tmp/a`b``")
	require.Contains(t, got, "- **a\\*b** — Use \\[docs\\] and \\<files\\>.")
	require.Contains(t, got, "_1 skill_")
}

func withListSkillsOutputTest(t *testing.T, tty, raw bool) {
	t.Helper()
	savedTTY := IsOutputTTY
	savedConfig := config
	savedRenderer := renderSkillsMarkdown
	IsOutputTTY = func() bool { return tty }
	config.Raw = raw
	t.Cleanup(func() {
		IsOutputTTY = savedTTY
		config = savedConfig
		renderSkillsMarkdown = savedRenderer
	})
}

func writeCLITestSkill(t *testing.T, root, name, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nInstructions.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
}
