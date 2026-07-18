package netutil

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

type staticResolver []net.IPAddr

func (r staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r, nil
}

type recordingDialer struct {
	addr string
	err  error
}

func (d *recordingDialer) DialContext(_ context.Context, _, addr string) (net.Conn, error) {
	d.addr = addr
	return nil, d.err
}

func TestSafeTransportRejectsResolvedPrivateAddress(t *testing.T) {
	dialer := &recordingDialer{err: errors.New("must not dial")}
	transport := SafeTransport(SafeTransportOptions{
		Resolver:    staticResolver{{IP: net.ParseIP("127.0.0.1")}},
		Dialer:      dialer,
		ErrorPrefix: "test",
	})

	_, err := transport.DialContext(context.Background(), "tcp", "public.example:443")
	require.ErrorContains(t, err, "refused to dial private")
	require.Empty(t, dialer.addr)
	require.Nil(t, transport.Proxy, "a proxy must not bypass target address validation")
	require.NotZero(t, transport.TLSHandshakeTimeout)
}

func TestSafeTransportPinsValidatedPublicAddress(t *testing.T) {
	dialer := &recordingDialer{err: errors.New("dial stopped")}
	transport := SafeTransport(SafeTransportOptions{
		Resolver:    staticResolver{{IP: net.ParseIP("203.0.113.9")}},
		Dialer:      dialer,
		ErrorPrefix: "test",
	})

	_, err := transport.DialContext(context.Background(), "tcp", "public.example:8443")
	require.EqualError(t, err, "dial stopped")
	require.Equal(t, "203.0.113.9:8443", dialer.addr)
}

func TestSafeTransportPrivateOptInUsesOriginalAddress(t *testing.T) {
	dialer := &recordingDialer{err: errors.New("dial stopped")}
	transport := SafeTransport(SafeTransportOptions{
		AllowPrivate: func() bool { return true },
		Resolver:     staticResolver{{IP: net.ParseIP("127.0.0.1")}},
		Dialer:       dialer,
	})

	_, err := transport.DialContext(context.Background(), "tcp", "localhost:8080")
	require.EqualError(t, err, "dial stopped")
	require.Equal(t, "localhost:8080", dialer.addr)
}
