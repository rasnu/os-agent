package main

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestProxyConn(t *testing.T) {
	// Create a pair of connected in-memory network connections
	client1, server1 := net.Pipe()
	client2, server2 := net.Pipe()

	// Run proxyConn on the server sides of both pipes
	go proxyConn(server1, server2)

	// We are now connected via client1 and client2 through the proxy

	// Test sending from client1 to client2
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

	// Test sending from client2 to client1
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

	// Test that closing one closes the other (via proxyConn defer Close)
	client1.Close()

	// Wait a moment for proxyConn to detect close and close server2
	time.Sleep(50 * time.Millisecond)

	// Writing to client2 should now fail because the proxy closed server2
	_, err = client2.Write([]byte("test"))
	if err == nil {
		t.Errorf("Expected error writing to closed connection")
	}
}
