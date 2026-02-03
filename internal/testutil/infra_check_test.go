package testutil

import (
	"net"
	"testing"
)

func TestCheckTCP(t *testing.T) {
	// Case 1: Check a random open port
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to listen on random port: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String() // e.g., 127.0.0.1:54321
	urlStr := "postgres://" + addr

	if !checkTCP(urlStr, "5432") {
		t.Errorf("checkTCP failed for open port %s", addr)
	}

	// Case 2: Check a closed port
	// We pick a port that is unlikely to be open.
	// Port 0 is invalid for Dial, but checkTCP logic will try to dial it if we pass it.
	// Let's use a high port that we didn't bind.
	// Or just close the listener and check the same address.
	ln.Close()

	// Re-check the same address, should be closed now.
	if checkTCP(urlStr, "5432") {
		t.Errorf("checkTCP succeeded for closed port %s", addr)
	}

	// Case 3: Invalid URL
	if checkTCP("://invalid-url", "5432") {
		t.Errorf("checkTCP succeeded for invalid URL")
	}
}
