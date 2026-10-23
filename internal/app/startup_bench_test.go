package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Excludes Go package init, terminal round trips, and provider/MCP networking.
func BenchmarkAppStartup(b *testing.B) {
	for _, count := range []int{0, 100} {
		for _, raw := range []bool{false, true} {
			b.Run(fmt.Sprintf("skills=%d/raw=%v", count, raw), func(b *testing.B) {
				root := b.TempDir()
				for i := 0; i < count; i++ {
					dir := filepath.Join(root, fmt.Sprintf("skill-%03d", i))
					if err := os.Mkdir(dir, 0o700); err != nil {
						b.Fatal(err)
					}
					content := fmt.Sprintf("---\nname: skill-%03d\ndescription: example\n---\n", i) + strings.Repeat("instructions\n", 10000)
					if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
						b.Fatal(err)
					}
				}
				cfg := defaultConfig()
				cfg.Raw = raw
				cfg.WorkingDir = root
				cfg.SkillsDirs = []string{root}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					model, err := New(context.Background(), &cfg, nil)
					if err != nil {
						b.Fatal(err)
					}
					model.quit()
				}
			})
		}
	}
}
