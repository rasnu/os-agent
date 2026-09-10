// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestProxyConn(t *testing.T) {
	client1, server1 := net.Pipe()
	client2, server2 := net.Pipe()

	go proxyConn(server1, server2)

	msg1 := []byte("Hello from client1")
	go func() {
		client1.Write(msg1)
	}()

	buf := make([]byte, 1024)
	n, err := client2.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read from client2: %v", err)
	}

	if !bytes.Equal(buf[:n], msg1) {
		t.Errorf("Expected %s, got %s", msg1, buf[:n])
	}

	msg2 := []byte("Hello from client2")
	go func() {
		client2.Write(msg2)
	}()

	n, err = client1.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read from client1: %v", err)
	}

	if !bytes.Equal(buf[:n], msg2) {
		t.Errorf("Expected %s, got %s", msg2, buf[:n])
	}

	client1.Close()
	time.Sleep(50 * time.Millisecond)

	_, err = client2.Write([]byte("test"))
	if err == nil {
		t.Errorf("Expected error writing to closed connection")
	}
}
