package cli

import (
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/prompts"
)

func listPrompts() {
	for i, prompt := range prompts.Builtin() {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("## %s\n\n%s\n", prompt.Name, strings.TrimRight(prompt.Default, "\r\n"))
	}
}
