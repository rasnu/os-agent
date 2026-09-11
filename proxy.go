package main

import (
	"io"
	"net"
)

// proxyConn copies data bi-directionally between two connections.
func proxyConn(c1, c2 net.Conn) {
	defer c1.Close()
	defer c2.Close()

	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(c1, c2)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(c2, c1)
		errc <- err
	}()
	<-errc
}
