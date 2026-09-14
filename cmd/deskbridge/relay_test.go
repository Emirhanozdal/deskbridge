package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRelayTLSAuthentication(t *testing.T) {
	for _, same := range []bool{true, false} {
		a, err := relayTLS(strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Repeat("a", 64)
		if !same {
			code = strings.Repeat("b", 64)
		}
		b, err := relayTLS(code)
		if err != nil {
			t.Fatal(err)
		}
		x, y := net.Pipe()
		server, client := tls.Server(x, a), tls.Client(y, b)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		done := make(chan error, 1)
		go func() { done <- server.HandshakeContext(ctx) }()
		err = client.HandshakeContext(ctx)
		if (err == nil) != same {
			t.Fatalf("same=%v handshake=%v", same, err)
		}
		cancel()
		x.Close()
		y.Close()
		<-done
	}
}

func TestRelayRejectsInvalidConfiguration(t *testing.T) {
	valid := relaySettings{URL: "https://relay.example.com", Code: strings.Repeat("a", 64), Side: "a"}
	if err := validateRelay(valid); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/?token=x"} {
		cfg := valid
		cfg.URL = address
		if validateRelay(cfg) == nil {
			t.Fatalf("accepted %s", address)
		}
	}
}

func TestCopyRelayBothDirections(t *testing.T) {
	a, left := net.Pipe()
	b, right := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(time.Second))
	b.SetDeadline(time.Now().Add(time.Second))
	done := make(chan struct{})
	go func() { copyRelay(left, right); close(done) }()
	go a.Write([]byte("hello"))
	data := make([]byte, 5)
	if _, err := io.ReadFull(b, data); err != nil || string(data) != "hello" {
		t.Fatalf("forward %q %v", data, err)
	}
	go b.Write([]byte("world"))
	if _, err := io.ReadFull(a, data); err != nil || string(data) != "world" {
		t.Fatalf("reverse %q %v", data, err)
	}
	a.Close()
	b.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("relay leaked")
	}
}
