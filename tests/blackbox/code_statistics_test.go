//go:build integration

package blackbox_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/evals"
)

type codeCounts struct {
	Files int `json:"files"`
	Lines int `json:"lines"`
}

type codeStatistics struct {
	Total      codeCounts            `json:"total"`
	Production codeCounts            `json:"production"`
	Tests      codeCounts            `json:"tests"`
	ByDir      map[string]codeCounts `json:"by_dir"`
}

// Uses the production approval path, including the classifier when needed.
// The harness is noninteractive: an unknown operation requiring approval fails
// visibly instead of silently disabling review. The never variant is a control.
func TestRealAIBlackBoxCodeStatistics(t *testing.T) {
	for _, mode := range []string{"auto", "never"} {
		t.Run(mode, func(t *testing.T) {
			h := newRealAIHarnessWithTools(t, true, true, mode)
			if !h.profile.filesystemTools {
				t.Skip("provider does not support tools")
			}
			if _, err := exec.LookPath("git"); err != nil {
				t.Skip("fixture requires git")
			}
			fixtures := map[string]string{"README.md": "# Fixture\nNot Go source.\n"}
			want := codeStatistics{ByDir: make(map[string]codeCounts)}
			for i := 0; i < 12; i++ {
				dir := []string{"internal/app", "internal/tools", "internal/space dir"}[i%3]
				name := fmt.Sprintf("part %02d.go", i)
				isTest := i%4 == 0
				if isTest {
					name = fmt.Sprintf("part %02d_test.go", i)
				}
				// Known physical lines, including blank lines and comments.
				content := "package fixture\n\n// Fixture source.\n" + strings.Repeat("// counted physical line\n", 7+i)
				fixtures[dir+"/"+name] = content
				lines := strings.Count(content, "\n")
				want.Total.Files++
				want.Total.Lines += lines
				group := &want.Production
				if isTest {
					group = &want.Tests
				}
				group.Files++
				group.Lines += lines
				byDir := want.ByDir[dir]
				byDir.Files++
				byDir.Lines += lines
				want.ByDir[dir] = byDir
			}
			write := func(name, content string) {
				t.Helper()
				path := filepath.Join(h.cwd, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range fixtures {
				write(name, content)
			}
			for _, args := range [][]string{{"init", "--quiet"}, {"-c", "core.autocrlf=false", "add", "--", "."}} {
				cmd := exec.Command("git", args...)
				cmd.Dir, cmd.Env = h.cwd, h.env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("initialize fixture: %v\n%s", err, out)
				}
			}
			fixtures["untracked.go"] = "package fixture\n// Must be excluded.\n"
			write("untracked.go", fixtures["untracked.go"])

			started := time.Now()
			stdout, _ := h.run(t, "", "统计这个 Git 项目已跟踪的 Go 文件数量和物理行数（包括空行、注释）。同时列出生产代码、_test.go 测试代码，以及按完整父目录分组的数量和行数。排除未跟踪文件，不修改项目，不安装工具。只返回 JSON，格式为 {\"total\":{\"files\":整数,\"lines\":整数},\"production\":{\"files\":整数,\"lines\":整数},\"tests\":{\"files\":整数,\"lines\":整数},\"by_dir\":{\"目录路径\":{\"files\":整数,\"lines\":整数}}}。")
			t.Logf("provider=%s model_override=%q review=%s elapsed=%s", h.profile.name, os.Getenv(modelOverrideEnv), mode, time.Since(started))
			var got codeStatistics
			decodeJSONObject(t, stdout, &got)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("incorrect counts: got %+v; want %+v", got, want)
			}
			for name, content := range fixtures {
				got, err := os.ReadFile(filepath.Join(h.cwd, filepath.FromSlash(name)))
				if err != nil || string(got) != content {
					t.Errorf("fixture was modified: %s (read error: %v)", name, err)
				}
			}
			report := logExecutionScore(t, h, evals.CaseCodeStatistics)
			if report.Metrics.SimplificationSuggestions != 0 {
				t.Error("complexity must not generate failed tool results")
			}
		})
	}
}
