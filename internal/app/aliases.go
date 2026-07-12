package app

import (
	"github.com/panjie/mods/internal/apperr"
	"github.com/panjie/mods/internal/approval"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/session"

	debugpkg "github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/tooling"
	"github.com/panjie/mods/internal/ui"
)

type modsError = apperr.Error
type Config = cfgpkg.Config
type API = cfgpkg.API
type Model = cfgpkg.Model
type ReviewMode = cfgpkg.ReviewMode
type DB = session.DB
type Session = session.Session
type Rule = approval.Rule
type RuleSet = approval.RuleSet
type Scope = approval.Scope
type AccessIntent = approval.AccessIntent
type AccessClass = approval.AccessClass
type Decision = approval.Decision
type ApprovalReviewMode = approval.ReviewMode
type Styles = ui.Styles
type Anim = ui.Anim
type SpinnerPhase = ui.SpinnerPhase

var newUserErrorf = apperr.NewUserErrorf
var NewID = session.NewID
var IDPattern = session.IDPattern
var IsInputTTY = ui.IsInputTTY
var IsOutputTTY = ui.IsOutputTTY
var IsErrorTTY = ui.IsErrorTTY
var isInputTTY = ui.IsInputTTY
var isOutputTTY = ui.IsOutputTTY
var IncreaseIndent = ui.IncreaseIndent
var CutPrompt = ui.CutPrompt
var ToolOperationLabel = ui.ToolOperationLabel
var ToolOperationArgs = ui.ToolOperationArgs
var ToolArgsSummary = ui.ToolArgsSummary
var ArgString = ui.ArgString
var OneLinePreview = ui.OneLinePreview
var ShellCommandPreview = ui.ShellCommandPreview
var TruncateOperationStatus = ui.TruncateOperationStatus
var RemoveWhitespace = ui.RemoveWhitespace
var ShellResultBlock = ui.ShellResultBlock
var MakeStyles = ui.MakeStyles
var NewAnim = ui.NewAnim
var HideCommandWindow = platform.HideCommandWindow
var BuildRegistry = tooling.BuildRegistry
var RulesForDirs = approval.RulesForDirs
var RulesAllowDirs = approval.RulesAllowDirs
var RulesAllowIntent = approval.RulesAllowIntent
var RulesLabel = approval.RulesLabel
var WorkspaceScope = approval.WorkspaceScope
var ExtractShellCommand = approval.ExtractShellCommand
var AccessRead = approval.AccessRead
var AccessWrite = approval.AccessWrite
var DecisionAllow = approval.DecisionAllow
var DecisionAsk = approval.DecisionAsk
var ClassifyAccess = approval.ClassifyAccess
var ExternalDirs = approval.ExternalDirs
var safeDirs = approval.SafeDirs
var ErrNoMatches = session.ErrNoMatches

const (
	ReviewNever  = cfgpkg.ReviewNever
	ReviewAuto   = cfgpkg.ReviewAuto
	ReviewAlways = cfgpkg.ReviewAlways

	ShortIDLength = session.ShortIDLength
	MinIDLength   = session.MinIDLength

	ToolSelectionRules  = cfgpkg.ToolSelectionRules
	MinimalSystemPrompt = cfgpkg.MinimalSystemPrompt

	PhaseConnecting = ui.PhaseConnecting
	PhaseStreaming  = ui.PhaseStreaming
	PhaseTool       = ui.PhaseTool
)

var debug = debugpkg.FacadeInstance
