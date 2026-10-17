package cli

import (
	"errors"
	"net"
	"os"
)

// Recovery guidance is based on typed errors, not provider message guesses.
func errorRecoveryHint(err error) string {
	var network net.Error
	switch {
	case errors.Is(err, errSetupCanceled):
		return ""
	case errors.Is(err, os.ErrPermission):
		return "Check permissions for the file or directory, then retry."
	case errors.Is(err, os.ErrNotExist):
		return "Check that the file or directory exists and the path is correct, then retry."
	case errors.As(err, &network):
		return "Check your network and provider endpoint in mods --config, then retry."
	}
	return ""
}
