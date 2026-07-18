package app

import (
	"github.com/panjie/mods/internal/approval"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/testutil"
	"github.com/panjie/mods/internal/tooling"
	"github.com/panjie/mods/internal/ui"
)

var makeStyles = ui.MakeStyles
var defaultConfig = cfgpkg.Default
var buildToolRegistry = tooling.BuildRegistry
var shouldEnableFilesystemTools = tooling.ShouldEnableFilesystemTools

const (
	approvalShellPrefix       = approval.ShellPrefix
	approvalEditAll           = approval.EditAll
	approvalDirAllow          = approval.DirAllow
	FilesystemAuto            = cfgpkg.FilesystemAuto
	FilesystemAlways          = cfgpkg.FilesystemAlways
	FilesystemNever           = cfgpkg.FilesystemNever
	minimalSystemPrompt       = cfgpkg.MinimalSystemPrompt
	defaultMarkdownFormatText = "Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it."
	defaultJSONFormatText     = "Return valid JSON only. Do not include Markdown fences, prose, or explanations unless the user explicitly requests them."
)

type approvalRuleSet = approval.RuleSet
type ApprovalRule = approval.Rule
type PersistentConfig = cfgpkg.PersistentConfig
type PromptConfig = cfgpkg.PromptConfig
type APIs = cfgpkg.APIs
type FormatText = cfgpkg.FormatText

var testDB = testutil.OpenTestDB
