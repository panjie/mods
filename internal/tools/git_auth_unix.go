//go:build !windows

package tools

import (
	"context"
	"net"
	"path/filepath"
)

func listenGitAuth(dir, _ string) (net.Listener, string, error) {
	address := filepath.Join(dir, "auth.sock")
	listener, err := net.Listen("unix", address)
	return listener, address, err
}

func dialGitAuth(ctx context.Context, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}
