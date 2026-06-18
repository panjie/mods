package cli

import "github.com/charmbracelet/mods/internal/apperr"
import "github.com/charmbracelet/mods/internal/app"
import cfgpkg "github.com/charmbracelet/mods/internal/config"
import "github.com/charmbracelet/mods/internal/conversation"
import debugpkg "github.com/charmbracelet/mods/internal/debug"
import "github.com/charmbracelet/mods/internal/mcpclient"
import "github.com/charmbracelet/mods/internal/platform"
import "github.com/charmbracelet/mods/internal/ui"

type modsError = apperr.Error
type Config = cfgpkg.Config
type API = cfgpkg.API
type Model = cfgpkg.Model
type ReasoningMode = cfgpkg.ReasoningMode
type ReviewMode = cfgpkg.ReviewMode
type DB = conversation.DB
type Conversation = conversation.Conversation
type Mods = app.Mods
type Styles = ui.Styles

var newUserErrorf = apperr.NewUserErrorf
var newMods = app.New
var Default = cfgpkg.Default
var defaultConfig = cfgpkg.Default
var Ensure = cfgpkg.Ensure
var ensureConfig = cfgpkg.Ensure
var WriteDefaultFile = cfgpkg.WriteDefaultFile
var writeConfigFile = cfgpkg.WriteDefaultFile
var help = cfgpkg.Help
var Help = cfgpkg.Help
var openDB = conversation.Open
var Open = conversation.Open
var newConversationID = conversation.NewID
var RemoveWhitespace = ui.RemoveWhitespace
var IsInputTTY = ui.IsInputTTY
var IsOutputTTY = ui.IsOutputTTY
var IsErrorTTY = ui.IsErrorTTY
var StdoutStyles = ui.StdoutStyles
var StderrStyles = ui.StderrStyles
var StdoutRenderer = ui.StdoutRenderer
var StderrRenderer = ui.StderrRenderer
var stdoutStyles = ui.StdoutStyles
var stderrStyles = ui.StderrStyles
var stdoutRenderer = ui.StdoutRenderer
var stderrRenderer = ui.StderrRenderer
var isInputTTY = ui.IsInputTTY
var isOutputTTY = ui.IsOutputTTY
var isErrorTTY = ui.IsErrorTTY
var PrintConfirmation = ui.PrintConfirmation
var printConfirmation = ui.PrintConfirmation
var FirstLine = ui.FirstLine
var HideCommandWindow = platform.HideCommandWindow
var hideCommandWindow = platform.HideCommandWindow
var List = mcpclient.List
var ListTools = mcpclient.ListTools

const (
	ReviewNever   = cfgpkg.ReviewNever
	ReviewMutable = cfgpkg.ReviewMutable
	ReviewAlways  = cfgpkg.ReviewAlways
	ReasoningOff  = cfgpkg.ReasoningOff
	ReasoningOn   = cfgpkg.ReasoningOn
	ReasoningAuto = cfgpkg.ReasoningAuto

	sha1short  = conversation.ShortIDLength
	sha1minLen = conversation.MinIDLength

	ShortIDLength = conversation.ShortIDLength
	MinIDLength   = conversation.MinIDLength
)

var sha1reg = conversation.IDPattern
var IDPattern = conversation.IDPattern
var lastPrompt = app.LastPrompt

type debugFacade struct{}

var debug debugFacade

func (debugFacade) SetEnabled(enabled bool)           { debugpkg.SetEnabled(enabled) }
func (debugFacade) Printf(format string, args ...any) { debugpkg.Printf(format, args...) }
func (debugFacade) Enabled() bool                     { return debugpkg.Enabled() }
func (debugFacade) Truncate(s string, max int) string { return debugpkg.Truncate(s, max) }
