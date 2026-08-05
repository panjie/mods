package google

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// ipv4FallbackTransport preserves the platform's normal dual-stack behavior,
// but retries transport-level failures over IPv4. This handles VPNs that
// advertise an unusable IPv6 route while IPv4 remains healthy.
type ipv4FallbackTransport struct {
	primary  http.RoundTripper
	fallback http.RoundTripper
}

func (t *ipv4FallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.primary.RoundTrip(req)
	if err == nil || req.Context().Err() != nil || !isTransportError(err) {
		return resp, err
	}

	retryReq, retryErr := cloneRequestForRetry(req)
	if retryErr != nil {
		return nil, err
	}
	return t.fallback.RoundTrip(retryReq)
}

func cloneRequestForRetry(req *http.Request) (*http.Request, error) {
	retryReq := req.Clone(req.Context())
	if req.Body == nil {
		return retryReq, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("request body cannot be replayed")
	}
	_ = req.Body.Close()
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	retryReq.Body = body
	return retryReq, nil
}

func isTransportError(err error) bool {
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func newHTTPClientWithIPv4Fallback() *http.Client {
	primary := http.DefaultTransport
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if defaultTransport, ok := primary.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}
	return &http.Client{Transport: &ipv4FallbackTransport{
		primary:  primary,
		fallback: transport,
	}}
}
