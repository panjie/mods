package app

import (
	"github.com/panjie/mods/internal/ui"
)

type interactionTone = ui.InteractionTone

const (
	interactionToneInfo    = ui.InteractionToneInfo
	interactionToneWarning = ui.InteractionToneWarning
	interactionToneDanger  = ui.InteractionToneDanger
	interactionToneSuccess = ui.InteractionToneSuccess
)

type interactionRow = ui.InteractionRow
type interactionAction = ui.InteractionAction
type interactionPanel = ui.InteractionPanel

func renderInteractionPanel(styles ui.InteractionStyles, width int, panel interactionPanel) string {
	return ui.RenderInteractionPanel(styles, width, panel)
}

func interactionPanelInnerWidth(styles ui.InteractionStyles, width int) int {
	return ui.InteractionPanelInnerWidth(styles, width)
}
