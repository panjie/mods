package evals

import (
	"embed"
	"encoding/json"
	"fmt"
)

// CommandGenerationCase separates a task's intent from a fixed parser control.
// Controls are only analyzed; they are not recipes shown to the model.
type CommandGenerationCase struct {
	ID                 string             `json:"id"`
	Prompt             string             `json:"prompt"`
	Query              bool               `json:"query"`
	ReadSolution       bool               `json:"read_solution"`
	PowerShell         string             `json:"powershell"`
	POSIX              string             `json:"posix"`
	Decision           string             `json:"decision"`
	PowerShellDecision string             `json:"powershell_decision,omitempty"`
	Check              string             `json:"check"`
	Expected           map[string]float64 `json:"expected,omitempty"`
}

//go:embed testdata/command_generation/*
var commandGenerationData embed.FS

func CommandGenerationCases() ([]CommandGenerationCase, error) {
	data, err := commandGenerationData.ReadFile("testdata/command_generation/cases.json")
	if err != nil {
		return nil, err
	}
	var cases []CommandGenerationCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func OriginalCodeStatisticsCommand() string {
	data, _ := commandGenerationData.ReadFile("testdata/command_generation/original.ps1")
	return string(data)
}

// CommandGenerationFiles returns fresh synthetic inputs, never user files.
// Counts include blank lines, CRLF, an unterminated last line and an empty file.
func CommandGenerationFiles() map[string]string {
	return map[string]string{
		"a.go":                 "package fixture\n\n// comment\n",
		"nested/space name.go": "package fixture\r\n\r\n// no final newline",
		"empty_test.go":        "",
		"amounts.csv":          "item,amount\na,2\nb,3\nc,5\n",
		"events.log":           "INFO ready\nERROR first\nINFO waiting\nERROR second\n",
		"settings.json":        "{\"enabled\":false}\n",
		"README.md":            "# Synthetic project\nNot source code.\n",
	}
}

// CheckGenerationAnswer only checks explicitly requested numeric fields.
// An ambiguous natural-language answer requires human review, not guessed scoring.
func CheckGenerationAnswer(answer string, expected map[string]float64) error {
	if len(expected) == 0 {
		return fmt.Errorf("manual correctness review required")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(answer), &got); err != nil {
		return err
	}
	for key, want := range expected {
		value, ok := got[key].(float64)
		if !ok || value != want {
			return fmt.Errorf("%s: got %v, want %v", key, got[key], want)
		}
	}
	return nil
}

// GenerationOutcome uses pointers for unavailable observations. In particular,
// first-round-only and noninteractive assessments are not UI approval events.
type GenerationOutcome struct {
	AutomaticReorganizations int   `json:"automatic_reorganizations"`
	ManualReorganizations    int   `json:"manual_reorganizations"`
	Query                    bool  `json:"query"`
	Completed                bool  `json:"completed"`
	Correct                  *bool `json:"correct"`
	ActualReviewPrompts      *int  `json:"actual_review_prompts"`
	ApprovalUnavailable      int   `json:"approval_unavailable"`
	// ToolErrors counts non-control errors returned by the production caller.
	// It is not proof that dispatch happened; a validation or analysis error
	// can also produce it. Execution failures require a trusted dispatch event.
	ToolErrors int `json:"tool_errors"`
	// AllowPathErrors is the subset of ToolErrors for calls the harness observed
	// as allow-classified immediately before the production caller. It is an
	// observed post-authorization error, not a trusted dispatch event.
	AllowPathErrors  int `json:"allow_path_errors"`
	ToolCalls        int `json:"tool_calls"`
	ToolRounds       int `json:"tool_rounds"`
	UnexpectedWrites int `json:"unexpected_writes"`
}

func (o *GenerationOutcome) ObserveReorganization(err error) {
	if automatic, control := ReorganizationKind(err); control {
		if automatic {
			o.AutomaticReorganizations++
		} else {
			o.ManualReorganizations++
		}
	}
}

func (o GenerationOutcome) ZeroReviewCompletion() *bool {
	if o.Correct == nil || o.ActualReviewPrompts == nil {
		return nil
	}
	result := o.Query && o.Completed && *o.Correct && *o.ActualReviewPrompts == 0 && o.ApprovalUnavailable == 0 && o.UnexpectedWrites == 0
	return &result
}
