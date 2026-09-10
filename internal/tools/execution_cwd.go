package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// NormalizeExecutionCwd resolves the read-only execution context identically
// before assessment and execution. A cwd is never evidence of a write target.
func NormalizeExecutionCwd(root, input string) (string, error) {
	if input == "" {
		input = root
	}
	if strings.IndexByte(input, 0) >= 0 {
		return "", fmt.Errorf("cwd contains a null byte")
	}
	p := pathutil.NormalizePath(input, pathutil.DefaultOptions(root, pathutil.FlavorPOSIX))
	p, err := pathutil.ResolveThroughExistingParent(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory")
	}
	return p, nil
}
