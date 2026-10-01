package main

import (
	"context"
	"net"
	"net/http"
	"time"
)

// HTTPDoer interface allows us to mock http.Client in tests
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type userAgentRoundTripper struct {
	inner http.RoundTripper
}

func (rt *userAgentRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "RasnuOSAgent/"+version)
	return rt.inner.RoundTrip(req)
}

func defaultTransport() http.RoundTripper {
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	
	// Force IPv4 for all agent outgoing requests to avoid broken IPv6 routes on customer VPSs
	// which commonly cause 10s timeouts when dialing Cloudflare AAAA records.
	baseTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		return dialer.DialContext(ctx, "tcp4", addr)
	}

	// Fix for mobile/SIM networks: Cellular NAT gateways aggressively drop idle connections.
	// If IdleConnTimeout is > 15s, the next metrics push will try to use a dead connection,
	// causing it to hang and timeout. Setting this to 5s forces a fresh dial for periodic metrics
	// while still allowing fast keep-alive reuse for high-frequency log streaming.
	baseTransport.IdleConnTimeout = 5 * time.Second

	return &userAgentRoundTripper{inner: baseTransport}
}

var (
	defaultHTTPClient      HTTPDoer = &http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()}
	heartbeatHTTPClient    HTTPDoer = &http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()}
	updaterHTTPClient      HTTPDoer = &http.Client{Timeout: 240 * time.Second, Transport: defaultTransport()}
	fileTransferHTTPClient HTTPDoer = &http.Client{Timeout: 0, Transport: defaultTransport()}
)
