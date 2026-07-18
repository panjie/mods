package netutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// Resolver is the subset of net.Resolver used by SafeTransport.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// ContextDialer is the subset of net.Dialer used by SafeTransport.
type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// SafeTransportOptions configures an HTTP transport that refuses to dial
// internal addresses after DNS resolution.
type SafeTransportOptions struct {
	AllowPrivate func() bool
	Resolver     Resolver
	Dialer       ContextDialer
	ErrorPrefix  string
}

// SafeTransport returns an HTTP transport that resolves hostnames itself,
// rejects every private/loopback/link-local result, and dials the selected IP
// directly. Pinning the connection to the checked IP prevents DNS rebinding
// between validation and connect.
func SafeTransport(opts SafeTransportOptions) *http.Transport {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := opts.Dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	prefix := opts.ErrorPrefix
	if prefix == "" {
		prefix = "http"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy would resolve and connect to the target outside this dial hook,
	// bypassing the address check. These security-sensitive clients therefore
	// connect directly.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if opts.AllowPrivate != nil && opts.AllowPrivate() {
			return dialer.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%s: host %q resolved to no addresses", prefix, host)
		}
		for _, resolved := range ips {
			if IsBlockedAddress(resolved.IP) {
				return nil, fmt.Errorf("%s: refused to dial private or loopback address %s for %q", prefix, resolved.IP, host)
			}
		}
		var dialErr error
		for _, resolved := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		return nil, dialErr
	}
	return transport
}

// IsBlockedAddress reports whether an address is unsafe for an
// Internet-facing HTTP client to dial.
func IsBlockedAddress(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}
