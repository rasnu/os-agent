// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"rasnuos-agent/internal/transport"
)

func TestPusherClient_AuthenticateChannel_Failures(t *testing.T) {
	origClient := transport.Default()
	defer func() {
		transport.SetDefault(origClient)
	}()

	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server500.Close()

	client1 := NewPusherClient("ws://", server500.URL, "token", "srv", "")
	transport.SetDefault(server500.Client())
	client1.authenticateChannel("socket1", "channel1")

	serverBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{bad json`))
	}))
	defer serverBad.Close()

	client2 := NewPusherClient("ws://", serverBad.URL, "token", "srv", "")
	transport.SetDefault(serverBad.Client())
	client2.authenticateChannel("socket2", "channel2")

	client3 := NewPusherClient("ws://", "http://localhost:0/fail", "token", "srv", "")
	client3.authenticateChannel("socket3", "channel3")
}
