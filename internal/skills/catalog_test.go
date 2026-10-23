package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogDefersBodyAndInvalidatesMetadata(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	require.NoError(t, os.Mkdir(dir, 0o700))
	path := filepath.Join(dir, "SKILL.md")
	write := func(text string) { require.NoError(t, os.WriteFile(path, []byte(text), 0o600)) }
	write("---\nname: demo\ndescription: original\n---\n" + strings.Repeat("body", 1<<20))
	catalog, err := ScanCatalogDirs([]string{root})
	require.NoError(t, err)
	require.Len(t, catalog, 1)
	require.Empty(t, catalog[0].Body)
	// Cache hits retain the same metadata but do not retain the old body.
	again, err := ScanCatalogDirs([]string{root})
	require.NoError(t, err)
	require.Equal(t, catalog, again)
	write("---\nname: demo\ndescription: changed metadata\n---\nnew instructions")
	body, err := catalog[0].LoadBody()
	require.NoError(t, err)
	require.Equal(t, "new instructions", body)
	again, err = ScanCatalogDirs([]string{root})
	require.NoError(t, err)
	require.Equal(t, "changed metadata", again[0].Description)
	write("---\nname: renamed\n---\nother instructions")
	_, err = catalog[0].LoadBody()
	require.ErrorContains(t, err, "renamed")
	require.NoError(t, os.Remove(path))
	again, err = ScanCatalogDirs([]string{root})
	require.NoError(t, err)
	require.Empty(t, again)
}

func TestCatalogMatchesFullScanMetadata(t *testing.T) {
	for _, text := range []string{
		"plain instructions", "---", "---\nunterminated", "---\r\nname: crlf\r\ndescription: >\r\n  first\r\n  second\r\n---\r\nbody",
		"---\nname: example\ndescription: |\n  one\n  two\n---\nbody",
	} {
		t.Run(text[:min(20, len(text))], func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(text), 0o600))
			full, err := readFullSkill(dir)
			require.NoError(t, err)
			metadata, err := readCatalogSkill(dir)
			require.NoError(t, err)
			require.Equal(t, full.Name, metadata.Name)
			require.Equal(t, full.Description, metadata.Description)
			body, err := metadata.LoadBody()
			require.NoError(t, err)
			require.Equal(t, full.Body, body)
		})
	}
}
