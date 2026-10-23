package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/chatgpt"
)

func runChatGPTAuth(ctx context.Context, action string, out io.Writer) error {
	c := chatgpt.NewClient()
	verb, id, _ := strings.Cut(action, ":")
	switch verb {
	case "login":
		if err := c.Login(ctx, id, func(u string) error {
			if err := openAuthBrowser(ctx, u); err != nil {
				_, writeErr := fmt.Fprintln(out, "Could not open your browser automatically. Open this URL to continue with ChatGPT:\n"+u)
				return writeErr
			}
			_, err := fmt.Fprintln(out, "Browser opened. Complete ChatGPT authorization in your browser.")
			return err
		}); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Signed in. Run mods --config and choose chatgpt to select a model.")
		return err
	case "list":
		st, err := c.Store.Load()
		if err != nil {
			return err
		}
		for _, a := range st.Accounts {
			mark := " "
			if a.ClientID == st.Active {
				mark = "*"
			}
			status := "signed out"
			if a.RefreshToken != "" {
				status = "signed in"
			}
			fmt.Fprintf(out, "%s %s  %s  %s\n", mark, a.ClientID, a.Email, status)
		}
		if len(st.Accounts) == 0 {
			fmt.Fprintln(out, "No ChatGPT accounts. Use --chatgpt-auth login.")
		}
		return nil
	case "use":
		return c.Store.Select(ctx, id)
	case "logout":
		return c.Logout(ctx)
	default:
		return fmt.Errorf("unknown ChatGPT auth action; use login, login:<client-id>, list, use:<client-id>, or logout")
	}
}
func (m *setupModel) authenticateChatGPT() tea.Cmd {
	ctx, result := m.begin("Starting ChatGPT authorization")
	return func() tea.Msg {
		urls := make(chan string, 1)
		done := make(chan error, 1)
		c := chatgpt.NewClient()
		st, err := c.Store.Load()
		if err != nil {
			result.err = err
			return result
		}
		go func() { done <- c.Login(ctx, st.Active, func(u string) error { urls <- u; return nil }) }()
		select {
		case u := <-urls:
			result.authURL = u
			result.browserErr = openAuthBrowser(ctx, u)
			result.authDone = done
		case err := <-done:
			result.err = err
		case <-ctx.Done():
			result.err = ctx.Err()
		}
		return result
	}
}
