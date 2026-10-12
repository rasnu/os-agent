package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPusherClient_AuthenticateChannel_Failures(t *testing.T) {
	origClient := getDefaultHTTPClient()
	defer func() {
		setDefaultHTTPClient(origClient)
	}()

	// 1. HTTP 500
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server500.Close()

	client1 := NewPusherClient("ws://", server500.URL, "token", "srv", "")
	setDefaultHTTPClient(server500.Client())
	client1.authenticateChannel("socket1", "channel1")

	// 2. Bad JSON
	serverBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{bad json`))
	}))
	defer serverBad.Close()

	client2 := NewPusherClient("ws://", serverBad.URL, "token", "srv", "")
	setDefaultHTTPClient(serverBad.Client())
	client2.authenticateChannel("socket2", "channel2")

	// 3. Network error
	client3 := NewPusherClient("ws://", "http://localhost:0/fail", "token", "srv", "")
	client3.authenticateChannel("socket3", "channel3")
}
