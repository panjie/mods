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
	if isCompletionCmd(args) || helpOrVersionRequested(args) || isAutoConfigSkippedAction() {
		return false, nil
	}
	hasSessions, err := db.HasSessions()
	if err != nil {
		return false, fmt.Errorf("check first-run sessions: %w", err)
	}
	return !hasSessions, nil
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

// isPassiveAutoConfigSkippedAction reports whether the invocation is a passive
// listing/help that must not leave an auto-created config file behind. The flag
// set is declared as roleBlocksPassiveAutoConfig in the flag table.
func isPassiveAutoConfigSkippedAction(args []string) bool {
	return isCompletionCmd(args) ||
		helpOrVersionRequested(args) ||
		showSkillsDirs ||
		anyRoleSelected(roleBlocksPassiveAutoConfig)
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

// isAutoConfigSkippedAction reports whether the invocation selects a one-shot
// action that should not trigger first-run auto configuration. The flag set is
// declared as roleBlocksAutoConfig in the flag table.
func isAutoConfigSkippedAction() bool {
	return showSkillsDirs || anyRoleSelected(roleBlocksAutoConfig)
}

func runAutoConfig() error {
	if err := runConfigWizard(); err != nil {
		return modsError{Err: err, ReasonText: "Configuration wizard failed."}
	}
	return newUserErrorf("Configuration complete. Please rerun your command.")
}
