// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package transport

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	"rasnuos-agent/internal/buildinfo"
)

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type userAgentRoundTripper struct {
	inner http.RoundTripper
}

func (rt *userAgentRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", "RasnuOSAgent/"+buildinfo.Version)
	return rt.inner.RoundTrip(req)
}

var (
	globalTLSConfig   *tls.Config
	globalTLSConfigMu sync.RWMutex
)

// SetGlobalTLSConfig updates the TLS configuration across all existing and future agent HTTP transports.
func SetGlobalTLSConfig(cfg *tls.Config) {
	globalTLSConfigMu.Lock()
	globalTLSConfig = cfg
	globalTLSConfigMu.Unlock()

	applyToClient := func(doer HTTPDoer) {
		if client, ok := doer.(*http.Client); ok && client != nil {
			if client.Transport == nil {
				client.Transport = defaultTransport()
			}
			if rt, ok := client.Transport.(*userAgentRoundTripper); ok && rt != nil {
				if t, ok := rt.inner.(*http.Transport); ok && t != nil {
					t.TLSClientConfig = cfg
					return
				}
			}
			if t, ok := client.Transport.(*http.Transport); ok && t != nil {
				t.TLSClientConfig = cfg
				return
			}
		}
	}

	httpClientMu.Lock()
	defer httpClientMu.Unlock()
	applyToClient(defaultClient)
	applyToClient(Heartbeat)
	applyToClient(Updater)
	applyToClient(FileTransfer)
}

func getGlobalTLSConfig() *tls.Config {
	globalTLSConfigMu.RLock()
	defer globalTLSConfigMu.RUnlock()
	return globalTLSConfig
}

func defaultTransport() http.RoundTripper {
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()

	if cfg := getGlobalTLSConfig(); cfg != nil {
		baseTransport.TLSClientConfig = cfg
	}

	// Dial tcp4 explicitly to avoid connection timeouts on hosts where DNS returns AAAA
	// records first but IPv6 egress routing is unconfigured.
	baseTransport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		return dialer.DialContext(ctx, "tcp4", addr)
	}

	// Keep IdleConnTimeout at 5s so 50ms log-streaming bursts reuse warm sockets while
	// 15s telemetry and 54s heartbeats establish fresh connections rather than hitting
	// idle sockets silently dropped by NAT gateways.
	baseTransport.IdleConnTimeout = 5 * time.Second

	return &userAgentRoundTripper{inner: baseTransport}
}

var (
	httpClientMu  sync.RWMutex
	defaultClient HTTPDoer = &http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()}
	Heartbeat     HTTPDoer = &http.Client{Timeout: 30 * time.Second, Transport: defaultTransport()}
	Updater       HTTPDoer = &http.Client{Timeout: 240 * time.Second, Transport: defaultTransport()}
	FileTransfer  HTTPDoer = &http.Client{Timeout: 0, Transport: defaultTransport()}
)

func Default() HTTPDoer {
	httpClientMu.RLock()
	defer httpClientMu.RUnlock()
	return defaultClient
}

func SetDefault(client HTTPDoer) {
	httpClientMu.Lock()
	defer httpClientMu.Unlock()
	defaultClient = client
}
