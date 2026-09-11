package main

import (
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
	req.Header.Set("User-Agent", "RasnuOneAgent/"+version)
	return rt.inner.RoundTrip(req)
}

func defaultTransport() http.RoundTripper {
	return &userAgentRoundTripper{inner: http.DefaultTransport}
}

var (
	defaultHTTPClient      HTTPDoer = &http.Client{Timeout: 10 * time.Second, Transport: defaultTransport()}
	heartbeatHTTPClient    HTTPDoer = &http.Client{Timeout: 5 * time.Second, Transport: defaultTransport()}
	updaterHTTPClient      HTTPDoer = &http.Client{Timeout: 240 * time.Second, Transport: defaultTransport()}
	fileTransferHTTPClient HTTPDoer = &http.Client{Timeout: 0, Transport: defaultTransport()}
)
