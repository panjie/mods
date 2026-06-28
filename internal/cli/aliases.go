package cli

import (
	"github.com/panjie/mods/internal/app"
	"github.com/panjie/mods/internal/apperr"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/conversation"
	debugpkg "github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/mcpclient"
	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/ui"
)

type (
	modsError     = apperr.Error
	Config        = cfgpkg.Config
	API           = cfgpkg.API
	Model         = cfgpkg.Model
	FieldUpdate   = cfgpkg.FieldUpdate
	ReasoningMode = cfgpkg.ReasoningMode
	ReviewMode    = cfgpkg.ReviewMode
	DB            = conversation.DB
	Conversation  = conversation.Conversation
	Mods          = app.Mods
	Styles        = ui.Styles
)

var (
	newUserErrorf     = apperr.NewUserErrorf
	newMods           = app.New
	Default           = cfgpkg.Default
	Ensure            = cfgpkg.Ensure
	WriteDefaultFile  = cfgpkg.WriteDefaultFile
	SaveFields        = cfgpkg.SaveFields
	SaveFieldPaths    = cfgpkg.SaveFieldPaths
	HasAPIKey         = cfgpkg.HasAPIKey
	Help              = cfgpkg.Help
	Open              = conversation.Open
	newConversationID = conversation.NewID
	RemoveWhitespace  = ui.RemoveWhitespace
	IsInputTTY        = ui.IsInputTTY
	IsOutputTTY       = ui.IsOutputTTY
	IsErrorTTY        = ui.IsErrorTTY
	StdoutStyles      = ui.StdoutStyles
	StderrStyles      = ui.StderrStyles
	StdoutRenderer    = ui.StdoutRenderer
	StderrRenderer    = ui.StderrRenderer
	PrintConfirmation = ui.PrintConfirmation
	FirstLine         = ui.FirstLine
	HideCommandWindow = platform.HideCommandWindow
	List              = mcpclient.List
	ListTools         = mcpclient.ListTools
)

const (
	ReviewNever   = cfgpkg.ReviewNever
	ReviewMutable = cfgpkg.ReviewMutable
	ReviewAlways  = cfgpkg.ReviewAlways
	ReasoningOff  = cfgpkg.ReasoningOff
	ReasoningOn   = cfgpkg.ReasoningOn
	ReasoningAuto = cfgpkg.ReasoningAuto

	ShortIDLength = conversation.ShortIDLength
	MinIDLength   = conversation.MinIDLength
)

var (
	IDPattern  = conversation.IDPattern
	lastPrompt = app.LastPrompt
)

type debugFacade struct{}

var debug debugFacade

func (debugFacade) SetEnabled(enabled bool)           { debugpkg.SetEnabled(enabled) }
func (debugFacade) Printf(format string, args ...any) { debugpkg.Printf(format, args...) }
func (debugFacade) Enabled() bool                     { return debugpkg.Enabled() }
func (debugFacade) Truncate(s string, max int) string { return debugpkg.Truncate(s, max) }
