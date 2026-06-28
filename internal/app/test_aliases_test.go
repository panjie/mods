package app

import (
	"testing"

	"github.com/panjie/mods/internal/approval"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/conversation"
	"github.com/panjie/mods/internal/tooling"
	"github.com/panjie/mods/internal/ui"
	"github.com/stretchr/testify/require"
)

var shellApprovalRulesWithMode = approval.ShellRulesWithMode
var shellApprovalRulesForToolWithMode = approval.ShellRulesForToolWithMode
var shellRulesAllowWithMode = approval.ShellAllowWithMode
var shellRulesAllowForToolWithMode = approval.ShellAllowForToolWithMode
var dedupeApprovalRules = approval.Dedupe
var newConversationID = conversation.NewID
var sha1reg = conversation.IDPattern
var newAnim = ui.NewAnim
var makeStyles = ui.MakeStyles
var defaultConfig = cfgpkg.Default
var buildToolRegistry = tooling.BuildRegistry
var shouldEnableFilesystemTools = tooling.ShouldEnableFilesystemTools
var lastPrompt = LastPrompt
var firstLine = FirstLine
var removeWhitespace = ui.RemoveWhitespace
var cutPrompt = ui.CutPrompt
var increaseIndent = ui.IncreaseIndent
var toolOperationLabel = ui.ToolOperationLabel
var truncateOperationStatus = ui.TruncateOperationStatus

const (
	approvalShellPrefix       = approval.ShellPrefix
	approvalShellExact        = approval.ShellExact
	approvalEditAll           = approval.EditAll
	approvalToolAll           = approval.ToolAll
	approvalDirAllow          = approval.DirAllow
	sha1short                 = conversation.ShortIDLength
	FilesystemAuto            = cfgpkg.FilesystemAuto
	FilesystemAlways          = cfgpkg.FilesystemAlways
	FilesystemNever           = cfgpkg.FilesystemNever
	minimalSystemPrompt       = cfgpkg.MinimalSystemPrompt
	defaultMarkdownFormatText = "Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it."
)

type approvalRuleSet = approval.RuleSet
type ApprovalRule = approval.Rule
type PersistentConfig = cfgpkg.PersistentConfig
type PromptConfig = cfgpkg.PromptConfig
type APIs = cfgpkg.APIs

func testDB(tb testing.TB) *conversation.DB {
	db, err := conversation.Open(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}
