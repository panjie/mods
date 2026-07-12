//go:build windows

package tools

import (
	"context"
	"fmt"
)

const SudoAskpassHelperArg = "__mods_sudo_askpass"

type preparedSudoCommand struct {
	Command string
	Env     map[string]string
}

func prepareSudoCommand(_ context.Context, command string, _ SecretPromptHandler) (preparedSudoCommand, func(), error) {
	return preparedSudoCommand{Command: command}, func() {}, nil
}

func RunSudoAskpassHelper([]string) error {
	return fmt.Errorf("sudo askpass is unavailable on Windows")
}
