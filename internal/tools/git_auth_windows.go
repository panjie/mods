//go:build windows

package tools

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenGitAuth(_ string, token string) (net.Listener, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", err
	}
	address := `\\.\pipe\mods-git-auth-` + token
	// A JSON decoder may finish before consuming the encoder's trailing
	// newline. Buffer bounded messages so request/response writes cannot
	// deadlock while the other side switches from decoding to encoding.
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")",
		InputBufferSize:    gitAuthMaxMessage,
		OutputBufferSize:   gitAuthMaxMessage,
	})
	return listener, address, err
}

func dialGitAuth(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}
