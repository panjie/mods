package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CommandEffect is the model's declaration, not a verified execution property.
type CommandEffect string

const (
	CommandReadOnly CommandEffect = "read_only"
	CommandMayWrite CommandEffect = "may_write"
)

// DeclaredCommandEffect validates the declaration without inspecting command text.
func DeclaredCommandEffect(data json.RawMessage) (CommandEffect, error) {
	var args struct {
		Effect CommandEffect `json:"effect"`
	}
	if err := json.Unmarshal(data, &args); err != nil {
		return "", fmt.Errorf("invalid command arguments: %w", err)
	}
	switch args.Effect {
	case CommandReadOnly, CommandMayWrite:
		return args.Effect, nil
	default:
		return "", fmt.Errorf("effect is required and must be read_only or may_write")
	}
}

func validateCommandArguments(data json.RawMessage) error {
	if _, err := DeclaredCommandEffect(data); err != nil {
		return err
	}
	var args struct {
		Purpose string `json:"purpose"`
	}
	if err := json.Unmarshal(data, &args); err != nil {
		return fmt.Errorf("invalid command purpose: %w", err)
	}
	if strings.TrimSpace(args.Purpose) == "" {
		return fmt.Errorf("purpose is required: briefly explain what this command does and why in the user's language")
	}
	return nil
}
