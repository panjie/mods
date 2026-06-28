package cli

import (
	"fmt"
	"os"
)

var runConfigWizard = RunConfigWizard

func shouldAutoConfig(args []string) (bool, error) {
	if config.SettingsExisted || db == nil {
		return false, nil
	}
	if isCompletionCmd(args) || isVersionOrHelpCmd(args) || isAutoConfigSkippedAction() {
		return false, nil
	}
	hasConversations, err := db.HasConversations()
	if err != nil {
		return false, fmt.Errorf("check first-run conversations: %w", err)
	}
	return !hasConversations, nil
}

func validateFirstRunPrerequisites(args []string) error {
	if err := validateChatMode(); err != nil {
		return err
	}
	return cleanupAutoCreatedConfig(args)
}

func cleanupAutoCreatedConfig(args []string) error {
	if config.SettingsExisted || config.SettingsPath == "" || !isPassiveAutoConfigSkippedAction(args) {
		return nil
	}
	if err := os.Remove(config.SettingsPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove auto-created config: %w", err)
	}
	return nil
}

func isPassiveAutoConfigSkippedAction(args []string) bool {
	return isCompletionCmd(args) ||
		isVersionOrHelpCmd(args) ||
		config.Dirs ||
		config.List ||
		config.ListRoles ||
		config.ListPrompts ||
		config.Show != "" ||
		config.ShowLast ||
		len(config.Delete) > 0 ||
		config.DeleteOlderThan != 0 ||
		config.MCPList ||
		config.MCPListTools
}

func maybeRunAutoConfig(args []string) (bool, error) {
	autoConfig, err := shouldAutoConfig(args)
	if err != nil {
		return false, modsError{Err: err, ReasonText: "Could not check first-run setup state."}
	}
	if !autoConfig {
		return false, nil
	}
	return true, runAutoConfig()
}

func isAutoConfigSkippedAction() bool {
	return config.Dirs ||
		config.Settings ||
		config.ConfigSetup ||
		config.ResetSettings ||
		config.List ||
		config.ListRoles ||
		config.ListPrompts ||
		config.Show != "" ||
		config.ShowLast ||
		len(config.Delete) > 0 ||
		config.DeleteOlderThan != 0 ||
		config.MCPList ||
		config.MCPListTools
}

func runAutoConfig() error {
	if err := runConfigWizard(); err != nil {
		return modsError{Err: err, ReasonText: "Configuration wizard failed."}
	}
	return newUserErrorf("Configuration complete. Please rerun your command.")
}
