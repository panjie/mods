package app

import "github.com/charmbracelet/mods/internal/approval"
import "github.com/charmbracelet/mods/internal/apperr"
import cfgpkg "github.com/charmbracelet/mods/internal/config"
import "github.com/charmbracelet/mods/internal/conversation"
import debugpkg "github.com/charmbracelet/mods/internal/debug"
import "github.com/charmbracelet/mods/internal/platform"
import "github.com/charmbracelet/mods/internal/tooling"
import "github.com/charmbracelet/mods/internal/ui"

type modsError = apperr.Error
type Config = cfgpkg.Config
type API = cfgpkg.API
type Model = cfgpkg.Model
type ReviewMode = cfgpkg.ReviewMode
type ReasoningMode = cfgpkg.ReasoningMode
type DB = conversation.DB
type Conversation = conversation.Conversation
type Rule = approval.Rule
type RuleSet = approval.RuleSet
type Styles = ui.Styles

var newUserErrorf = apperr.NewUserErrorf
var NewID = conversation.NewID
var IDPattern = conversation.IDPattern
var IsInputTTY = ui.IsInputTTY
var IsOutputTTY = ui.IsOutputTTY
var IsErrorTTY = ui.IsErrorTTY
var isInputTTY = ui.IsInputTTY
var isOutputTTY = ui.IsOutputTTY
var isErrorTTY = ui.IsErrorTTY
var IncreaseIndent = ui.IncreaseIndent
var CutPrompt = ui.CutPrompt
var ToolOperationLabel = ui.ToolOperationLabel
var ToolOperationArgs = ui.ToolOperationArgs
var ToolArgsSummary = ui.ToolArgsSummary
var ArgString = ui.ArgString
var OneLinePreview = ui.OneLinePreview
var TruncateOperationStatus = ui.TruncateOperationStatus
var RemoveWhitespace = ui.RemoveWhitespace
var MakeStyles = ui.MakeStyles
var NewAnim = ui.NewAnim
var HideCommandWindow = platform.HideCommandWindow
var BuildRegistry = tooling.BuildRegistry
var RulesFor = approval.RulesFor
var RulesLabel = approval.RulesLabel
var ExtractShellCommand = approval.ExtractShellCommand
var ErrNoMatches = conversation.ErrNoMatches

const (
	ReviewNever   = cfgpkg.ReviewNever
	ReviewMutable = cfgpkg.ReviewMutable
	ReviewAlways  = cfgpkg.ReviewAlways
	ReasoningOff  = cfgpkg.ReasoningOff
	ReasoningOn   = cfgpkg.ReasoningOn
	ReasoningAuto = cfgpkg.ReasoningAuto

	ShortIDLength = conversation.ShortIDLength
	MinIDLength   = conversation.MinIDLength

	ToolSelectionRules  = cfgpkg.ToolSelectionRules
	MinimalSystemPrompt = cfgpkg.MinimalSystemPrompt
)

type debugFacade struct{}

var debug debugFacade

func (debugFacade) Printf(format string, args ...any) { debugpkg.Printf(format, args...) }
func (debugFacade) Enabled() bool                     { return debugpkg.Enabled() }
func (debugFacade) Truncate(s string, max int) string { return debugpkg.Truncate(s, max) }
