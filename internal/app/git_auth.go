package app

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func gitAuthDisplayText(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
	text = redactRemoteURLsForDisplay(text)
	runes := []rune(text)
	if len(runes) > 512 {
		text = string(runes[:512]) + "…"
	}
	return text
}

func (m *Mods) handleGitAuthPrompt(ctx context.Context, req toolregistry.GitAuthRequest) (string, error) {
	kind, question := "secret", "Password or token"
	if req.Username {
		kind, question = "text", "Username"
	}
	clean := func(value string) string {
		if m.secrets != nil {
			value = m.secrets.Redact(value)
		}
		return gitAuthDisplayText(value)
	}
	rows := []interactionRow{
		{Label: "Git request", Value: clean(req.Prompt)},
		{Label: "Command", Value: clean(req.Command)},
	}
	if deadline, ok := ctx.Deadline(); ok {
		rows = append(rows, interactionRow{Label: "Timeout", Value: fmt.Sprintf("Input counts toward the command deadline (%s remaining)", max(time.Second, time.Until(deadline).Round(time.Second)))})
	}
	resp, err := m.userInput.requestWithDisplay(ctx, toolregistry.UserInputRequest{Question: question, Kind: kind}, userInputDisplay{
		title: "Git authentication", tone: interactionToneInfo,
		headline: question, rows: rows,
	})
	if err != nil {
		return "", err
	}
	// Even usernames may be sensitive. Register before returning any input to
	// the child, so immediate output/progress is already covered by redaction.
	if m.secrets != nil {
		m.secrets.RememberForRedaction(resp.Answer)
	}
	return resp.Answer, nil
}
