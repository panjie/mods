package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestListSkillsOutputsSortedCatalogAndSummary(t *testing.T) {
	withListOutputTest(t, false, false, 100)
	dir := t.TempDir()
	writeCLITestSkill(t, dir, "zeta", "Zeta skill. More detail that should be hidden.")
	writeCLITestSkill(t, dir, "alpha", "Alpha skill. More detail that should be hidden.")

	output := captureStdout(t, func() {
		require.NoError(t, listSkills([]string{dir}))
	})

	require.Equal(t,
		"Skills\n\nDirectory  "+dir+"\n\nNAME   DESCRIPTION\nalpha  Alpha skill.\nzeta   Zeta skill.\n\n2 skills\n",
		output,
	)
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
	withListOutputTest(t, false, false, 100)
	for _, dir := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		output := captureStdout(t, func() {
			require.NoError(t, listSkills([]string{dir}))
		})
		require.Equal(t,
			"Skills\n\nDirectory  "+dir+"\n\nNo skills found.\n\n0 skills\n",
			output,
		)
	}
}

func TestListSkillsUsesMergedDirectories(t *testing.T) {
	withListOutputTest(t, false, false, 100)
	first := t.TempDir()
	second := t.TempDir()
	writeCLITestSkill(t, first, "shared", "Shared first.")
	writeCLITestSkill(t, second, "shared", "Shared second.")
	writeCLITestSkill(t, second, "project", "Project skill.")

	output := captureStdout(t, func() {
		require.NoError(t, listSkills([]string{first, second}))
	})

	require.Contains(t, output, "Directories\n  "+first+"\n  "+second)
	require.Contains(t, output, "project  Project skill.")
	require.Contains(t, output, "shared   Shared second.")
}

func TestListSkillsScanFailureReturnsError(t *testing.T) {
	saved := scanSkills
	scanSkills = func([]string) ([]skills.Skill, error) {
		return nil, errors.New("permission denied")
	}
	t.Cleanup(func() { scanSkills = saved })

	err := listSkills([]string{"/unreadable"})

	require.Error(t, err)
	merr, ok := err.(modsError)
	require.True(t, ok)
	require.Equal(t, "Could not scan skills directories.", merr.ReasonText)
	require.ErrorContains(t, merr.Err, "permission denied")
}

func TestListSkillsTTYUsesStylesAndRawDoesNot(t *testing.T) {
	dir := t.TempDir()
	writeCLITestSkill(t, dir, "alpha", "Alpha skill.")

	withListOutputTest(t, true, false, 100)
	styled := captureStdout(t, func() {
		require.NoError(t, listSkills([]string{dir}))
	})
	require.Contains(t, styled, "\x1b[")
	require.Contains(t, ansi.Strip(styled), "alpha  Alpha skill.")

	config.Raw = true
	raw := captureStdout(t, func() {
		require.NoError(t, listSkills([]string{dir}))
	})
	require.NotContains(t, raw, "\x1b[")
	require.Contains(t, raw, "alpha  Alpha skill.")
}

func TestNormalizeListDescriptionCollapsesWhitespace(t *testing.T) {
	require.Equal(t, "Use [docs] and <files>.", normalizeListDescription(" Use [docs]\n and\t<files>. "))
}

func withListOutputTest(t *testing.T, tty, raw bool, width int) {
	t.Helper()
	savedTTY := IsOutputTTY
	savedConfig := config
	savedWidth := listOutputWidth
	IsOutputTTY = func() bool { return tty }
	config.Raw = raw
	listOutputWidth = func() int { return width }
	t.Cleanup(func() {
		IsOutputTTY = savedTTY
		config = savedConfig
		listOutputWidth = savedWidth
	})
}

func writeCLITestSkill(t *testing.T, root, name, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\nInstructions.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600))
}

func TestSkillsDescriptionsDoNotContainMarkdownEscapes(t *testing.T) {
	withListOutputTest(t, false, false, 100)
	saved := scanSkills
	scanSkills = func([]string) ([]skills.Skill, error) {
		return []skills.Skill{{Name: "a*b", Description: "Use [docs] and <files>. More details."}}, nil
	}
	t.Cleanup(func() { scanSkills = saved })

	output := captureStdout(t, func() {
		require.NoError(t, listSkills([]string{"/tmp/a`b"}))
	})
	require.Contains(t, output, "Directory  /tmp/a`b")
	require.Contains(t, output, "a*b   Use [docs] and <files>.")
	require.False(t, strings.Contains(output, `\[`))
}
