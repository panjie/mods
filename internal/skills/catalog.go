package skills

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxFrontmatterBytes = 1 << 20
const maxCatalogCacheEntries = 1024

type catalogEntry struct {
	info  os.FileInfo
	skill Skill
}

var catalogCache = struct {
	sync.Mutex
	entries map[string]catalogEntry
}{entries: make(map[string]catalogEntry)}

// ScanCatalogDirs reads only frontmatter. Directory enumeration is repeated so
// additions/removals are visible; unchanged files reuse parsed metadata. The
// bounded process-local cache never stores skill bodies.
func ScanCatalogDirs(dirs []string) ([]Skill, error) {
	return scanDirs(dirs, func(dir string) ([]Skill, error) { return scan(dir, readCatalogSkill) })
}

func readFullSkill(dir string) (Skill, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	return parseSkill(string(data), dir)
}

// LoadBody reads current instructions on demand. A renamed skill must be
// rediscovered rather than silently loading a different skill under an old name.
func (s Skill) LoadBody() (string, error) {
	if !s.lazyBody {
		return s.Body, nil
	}
	current, err := readFullSkill(s.Dir)
	if err != nil {
		return "", err
	}
	if current.Name != s.Name {
		return "", fmt.Errorf("skill %q was renamed; refresh the skill catalog", s.Name)
	}
	return current.Body, nil
}

func readCatalogSkill(dir string) (Skill, error) {
	path, err := filepath.Abs(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer file.Close() //nolint:errcheck
	info, err := file.Stat()
	if err != nil {
		return Skill{}, err
	}
	catalogCache.Lock()
	cached, ok := catalogCache.entries[path]
	catalogCache.Unlock()
	if ok && os.SameFile(info, cached.info) && info.Size() == cached.info.Size() && info.ModTime().Equal(cached.info.ModTime()) {
		skill := cached.skill
		skill.Dir = dir
		return skill, nil
	}
	reader := bufio.NewReader(file)
	prefix, _ := reader.Peek(4)
	var text strings.Builder
	if string(prefix) == "---\n" || string(prefix) == "---\r" {
		scanner := bufio.NewScanner(io.LimitReader(reader, maxFrontmatterBytes+1))
		scanner.Buffer(make([]byte, 4096), maxFrontmatterBytes+1)
		for scanner.Scan() {
			line := scanner.Text()
			if text.Len() == 0 && line != "---" {
				break
			}
			closing := text.Len() > 0 && strings.TrimSpace(line) == "---"
			text.WriteString(line)
			text.WriteByte('\n')
			if text.Len() > maxFrontmatterBytes {
				return Skill{}, fmt.Errorf("skill frontmatter too large: %s", path)
			}
			if closing {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return Skill{}, err
		}
	}
	skill, err := parseSkill(text.String(), dir)
	if err != nil {
		return Skill{}, err
	}
	skill.Body, skill.lazyBody = "", true
	catalogCache.Lock()
	if len(catalogCache.entries) >= maxCatalogCacheEntries {
		clear(catalogCache.entries)
	}
	catalogCache.entries[path] = catalogEntry{info: info, skill: skill}
	catalogCache.Unlock()
	return skill, nil
}
