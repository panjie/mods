//go:build integration

package blackbox_test

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func TestRealAIBlackBoxNativeImageSearch(t *testing.T) {
	if selectedProfile == nil || selectedProfile.name != "deepseek" {
		t.Skip("requires selected DeepSeek provider and key")
	}
	t.Setenv(modelOverrideEnv, "deepseek-flash")
	for _, endpoint := range []string{"chat-completions", "responses"} {
		t.Run(endpoint, func(t *testing.T) {
			h := newRealAIHarnessWithTools(t, true, false, "auto")
			for _, entry := range h.env {
				if configHome, ok := strings.CutPrefix(entry, "XDG_CONFIG_HOME="); ok {
					path := filepath.Join(configHome, "mods", "mods.yml")
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					config := strings.ReplaceAll(string(data), "filesystem: true", "filesystem: auto")
					config = strings.ReplaceAll(config, "deepseek-flash: {}", "deepseek-flash:\n        endpoint: "+endpoint)
					if err := os.WriteFile(path, []byte(config), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			writeVisionFixture(t, filepath.Join(h.cwd, "nested", "one.png"), "ORCHID RECEIPT 7341")
			writeVisionFixture(t, filepath.Join(h.cwd, "two.png"), "CEDAR TICKET 5926")
			for _, name := range []string{"three.png", "four.png"} {
				data, err := os.ReadFile(filepath.Join("testdata", "vision", name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.cwd, "nested", name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			stdout, _ := h.run(t, "", "找出当前目录及子目录中包含 ORCHID 或报销凭证的所有图片，只返回 JSON 对象：matches 数组中每项包含 path 和 text，text 要包含图片上完整的文字和编号作为证据；unread 数组列出未能读取的文件路径。")
			var result struct {
				Matches []struct {
					Path string `json:"path"`
					Text string `json:"text"`
				} `json:"matches"`
				Unread []string `json:"unread"`
			}
			decodeJSONObject(t, stdout, &result)

			if len(result.Matches) != 2 || len(result.Unread) != 0 {
				t.Fatalf("incorrect visual search result: %s", stdout)
			}
			matched := map[string]string{}
			for _, m := range result.Matches {
				matched[filepath.Base(m.Path)] = m.Text
			}
			if !strings.Contains(matched["one.png"], "7341") || !strings.Contains(matched["three.png"], "9286") || !strings.Contains(matched["three.png"], "报销凭证") {
				t.Fatalf("missing pixel-only evidence: %s", stdout)
			}
			t.Logf("%s: matched English and Chinese images with pixel-only evidence 7341 and 9286", endpoint)

		})
	}
}

func writeVisionFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	small := image.NewRGBA(image.Rect(0, 0, 240, 40))
	draw.Draw(small, small.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	drawer := font.Drawer{Dst: small, Src: image.NewUniform(color.Black), Face: basicfont.Face7x13, Dot: fixed.P(8, 24)}
	drawer.DrawString(text)
	large := image.NewRGBA(image.Rect(0, 0, 960, 160))
	xdraw.NearestNeighbor.Scale(large, large.Bounds(), small, small.Bounds(), draw.Src, nil)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, large); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
