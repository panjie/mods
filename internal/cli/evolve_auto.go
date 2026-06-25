package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/evolution"
)

const modsModulePath = "github.com/panjie/mods"

var (
	runEvolutionEvaluationForm = promptEvolutionEvaluation
	runEvolutionAutoImprove    = runAutomaticEvolutionImprovement
)

type evolutionEvaluationInput struct {
	Rating   int
	Feedback string
}

func maybeCollectEvolutionEvaluation(ctx context.Context, mods *Mods, db *DB) error {
	if db == nil || !shouldPromptEvolutionEvaluation(mods.Config) {
		return nil
	}
	input, err := runEvolutionEvaluationForm(mods.Config)
	if errorsIsUserAbort(err) {
		return nil
	}
	if err != nil {
		return modsError{Err: err, ReasonText: "Could not collect evolution feedback."}
	}
	workspace := mods.Config.ResolveWorkspaceRoot()
	triggered := shouldTriggerAutoImprove(mods.Config, input.Rating)
	evaluation, err := db.SaveEvolutionEvaluation(evolution.Evaluation{
		Workspace:      workspace,
		ConversationID: mods.Config.CacheWriteToID,
		Rating:         input.Rating,
		Feedback:       input.Feedback,
		Triggered:      triggered,
		Status:         evolution.EvaluationRecorded,
	})
	if err != nil {
		return modsError{Err: err, ReasonText: "Could not save evolution feedback."}
	}
	if !triggered {
		return nil
	}
	if _, err := db.UpdateEvolutionEvaluationStatus(workspace, evaluation.ID, evolution.EvaluationImproving, ""); err != nil {
		return modsError{Err: err, ReasonText: "Could not mark automatic improvement started."}
	}
	if !mods.Config.Quiet {
		fmt.Fprintf(os.Stderr, "\nStarting automatic improvement for evaluation %s.\n", shortID(evaluation.ID))
	}
	if err := runEvolutionAutoImprove(ctx, mods, db, evaluation); err != nil {
		if _, updateErr := db.UpdateEvolutionEvaluationStatus(workspace, evaluation.ID, evolution.EvaluationFailed, err.Error()); updateErr != nil {
			return modsError{Err: updateErr, ReasonText: "Could not mark automatic improvement failed."}
		}
		return modsError{Err: err, ReasonText: "Automatic improvement failed."}
	}
	if _, err := db.UpdateEvolutionEvaluationStatus(workspace, evaluation.ID, evolution.EvaluationVerified, ""); err != nil {
		return modsError{Err: err, ReasonText: "Could not mark automatic improvement verified."}
	}
	if !mods.Config.Quiet {
		fmt.Fprintf(os.Stderr, "Automatic improvement verified: %s\n", shortID(evaluation.ID))
	}
	return nil
}

func errorsIsUserAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

func shouldPromptEvolutionEvaluation(cfg *Config) bool {
	return cfg.EvolveAuto &&
		!cfg.Quiet &&
		!cfg.Raw &&
		!cfg.NoCache &&
		cfg.CacheWriteToID != "" &&
		IsInputTTY() &&
		IsOutputTTY()
}

func shouldTriggerAutoImprove(cfg *Config, rating int) bool {
	if !cfg.EvolveAuto {
		return false
	}
	threshold := cfg.EvolveThreshold
	if threshold == 0 {
		threshold = 3
	}
	return rating <= threshold
}

func promptEvolutionEvaluation(cfg *Config) (evolutionEvaluationInput, error) {
	var rating string
	var feedback string
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewText().
				Title("Feedback").
				Value(&feedback),
			huh.NewSelect[string]().
				Title("Rate this response").
				Options(
					huh.NewOption("5 - excellent", "5"),
					huh.NewOption("4 - good", "4"),
					huh.NewOption("3 - usable", "3"),
					huh.NewOption("2 - poor", "2"),
					huh.NewOption("1 - failed", "1"),
				).
				Value(&rating),
		),
	).
		WithTheme(themeFrom(cfg.Theme)).
		Run(); err != nil {
		return evolutionEvaluationInput{}, err
	}
	switch rating {
	case "1":
		return evolutionEvaluationInput{Rating: 1, Feedback: feedback}, nil
	case "2":
		return evolutionEvaluationInput{Rating: 2, Feedback: feedback}, nil
	case "3":
		return evolutionEvaluationInput{Rating: 3, Feedback: feedback}, nil
	case "4":
		return evolutionEvaluationInput{Rating: 4, Feedback: feedback}, nil
	case "5":
		return evolutionEvaluationInput{Rating: 5, Feedback: feedback}, nil
	default:
		return evolutionEvaluationInput{}, fmt.Errorf("rating is required")
	}
}

func runAutomaticEvolutionImprovement(ctx context.Context, mods *Mods, db *DB, evaluation evolution.Evaluation) error {
	workspace := mods.Config.ResolveWorkspaceRoot()
	if err := validateModsWorkspace(workspace); err != nil {
		return err
	}
	autoCfg := *mods.Config
	autoCfg.Prefix = automaticEvolutionPrompt(mods, evaluation)
	autoCfg.Plan = false
	autoCfg.NoCache = true
	autoCfg.EvolveAuto = false
	autoCfg.EvolveAutoImprove = true
	autoCfg.ReviewMode = ReviewMutable
	autoCfg.BuiltinTools.Workspace = workspace
	autoCfg.BuiltinTools.Filesystem = cfgpkg.FilesystemAlways
	autoCfg.BuiltinTools.Shell = true
	autoMods, err := newMods(ctx, StderrRenderer(), &autoCfg, db)
	if err != nil {
		return err
	}
	program := tea.NewProgram(autoMods, tea.WithInput(nil), tea.WithoutRenderer())
	result, err := program.Run()
	if err != nil {
		return err
	}
	autoMods = result.(*Mods)
	if autoMods.Error != nil {
		return autoMods.Error
	}
	return runEvolutionValidation(ctx, workspace)
}

func validateModsWorkspace(workspace string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	goModPath := filepath.Join(workspace, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("automatic improvement requires the mods workspace: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			if fields[1] == modsModulePath {
				return nil
			}
			return fmt.Errorf("automatic improvement refused outside %s workspace: found module %s", modsModulePath, fields[1])
		}
	}
	return fmt.Errorf("automatic improvement refused: go.mod has no module declaration")
}

func automaticEvolutionPrompt(mods *Mods, evaluation evolution.Evaluation) string {
	var b strings.Builder
	b.WriteString("Improve mods based on the completed session evaluation.\n\n")
	b.WriteString("Boundaries:\n")
	b.WriteString("- Work only inside the current mods workspace.\n")
	b.WriteString("- Do not edit files outside the workspace, user home config, system config, or global settings.\n")
	b.WriteString("- Do not create or use an evolution proposal, approval workflow, or plan approval gate.\n")
	b.WriteString("- Make the smallest code, test, or documentation change that directly addresses the feedback.\n")
	b.WriteString("- Run relevant tests; if the affected area is unclear, run task check and task test.\n")
	b.WriteString("- Stop and explain if the request cannot be implemented safely within the workspace.\n\n")
	fmt.Fprintf(&b, "Workspace: %s\n", mods.Config.ResolveWorkspaceRoot())
	fmt.Fprintf(&b, "Conversation ID: %s\n", mods.Config.CacheWriteToID)
	fmt.Fprintf(&b, "Rating: %d\n", evaluation.Rating)
	writeAutoSection(&b, "User Feedback", evaluation.Feedback)
	writeAutoSection(&b, "Original Request", lastPrompt(mods.Messages()))
	writeAutoSection(&b, "Model Output", mods.Output)
	return strings.TrimSpace(b.String())
}

func writeAutoSection(b *strings.Builder, title, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "(empty)"
	}
	fmt.Fprintf(b, "\n%s:\n%s\n", title, value)
}

func runEvolutionValidation(ctx context.Context, workspace string) error {
	for _, task := range []string{"check", "test"} {
		cmd := exec.CommandContext(ctx, "go", "run", "github.com/go-task/task/v3/cmd/task@v3.51.1", task)
		cmd.Dir = workspace
		HideCommandWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("task %s failed: %w\n%s", task, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
