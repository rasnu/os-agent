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

	return &userAgentRoundTripper{inner: baseTransport}
}

var (
	defaultHTTPClient      HTTPDoer = &http.Client{Timeout: 10 * time.Second, Transport: defaultTransport()}
	heartbeatHTTPClient    HTTPDoer = &http.Client{Timeout: 5 * time.Second, Transport: defaultTransport()}
	updaterHTTPClient      HTTPDoer = &http.Client{Timeout: 240 * time.Second, Transport: defaultTransport()}
	fileTransferHTTPClient HTTPDoer = &http.Client{Timeout: 0, Transport: defaultTransport()}
)
