package self

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptLooksFileRelated(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"english keyword edit", "please edit the config file", true},
		{"english keyword repo", "scan this repo for issues", true},
		{"chinese keyword modify", "请修改这个文件", true},
		{"chinese keyword code", "看看这段代码", true},
		{"path like posix", "look at src/app/main.go", true},
		{"path like dotted", "open config.yaml", true},
		{"unrelated question", "what is the meaning of life", false},
		{"empty prompt", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, PromptLooksFileRelated(tc.prompt))
		})
	}
}
