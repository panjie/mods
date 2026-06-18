package app

import (
	"testing"

	"github.com/charmbracelet/mods/internal/approval"
	cfgpkg "github.com/charmbracelet/mods/internal/config"
	"github.com/charmbracelet/mods/internal/conversation"
	"github.com/charmbracelet/mods/internal/tooling"
	"github.com/charmbracelet/mods/internal/ui"
	"github.com/stretchr/testify/require"
)

var shellApprovalRulesWithMode = approval.ShellRulesWithMode
var shellApprovalRulesForToolWithMode = approval.ShellRulesForToolWithMode
var shellRulesAllowWithMode = approval.ShellAllowWithMode
var shellRulesAllowForToolWithMode = approval.ShellAllowForToolWithMode
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
	sha1short                 = conversation.ShortIDLength
	FilesystemAuto            = cfgpkg.FilesystemAuto
	FilesystemAlways          = cfgpkg.FilesystemAlways
	FilesystemNever           = cfgpkg.FilesystemNever
	minimalSystemPrompt       = cfgpkg.MinimalSystemPrompt
	defaultMarkdownFormatText = "Format the response as markdown without enclosing backticks."
)

type approvalRuleSet = approval.RuleSet
type ApprovalRule = approval.Rule
type PersistentConfig = cfgpkg.PersistentConfig
type APIs = cfgpkg.APIs

func testDB(tb testing.TB) *conversation.DB {
	db, err := conversation.Open(":memory:")
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, db.Close())
	})
	return db
}
