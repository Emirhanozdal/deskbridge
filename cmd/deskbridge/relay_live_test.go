package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

func TestLiveRelayFileTransfer(t *testing.T) {
	endpoint := os.Getenv("DESKBRIDGE_TEST_RELAY")
	if endpoint == "" {
		t.Skip("set DESKBRIDGE_TEST_RELAY and DESKBRIDGE_TEST_CODE_FILE for live relay verification")
	}
	secret, err := os.ReadFile(os.Getenv("DESKBRIDGE_TEST_CODE_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimSpace(string(secret))
	if strings.HasPrefix(code, "{") {
		var cfg relaySettings
		if err := json.Unmarshal(secret, &cfg); err != nil {
			t.Fatal(err)
		}
		code = cfg.Code
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	connections := make([]*websocket.Conn, 2)
	for i, side := range []string{"a", "b"} {
		ws, _, err := websocket.Dial(ctx, strings.Replace(endpoint, "https://", "wss://", 1)+"/connect/"+side, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + relayAuth(code)}}})
		if err != nil {
			t.Fatal(err)
		}
		defer ws.CloseNow()
		ws.SetReadLimit(1048576)
		connections[i] = ws
	}
	for _, ws := range connections {
		kind, msg, err := ws.Read(ctx)
		if err != nil || kind != websocket.MessageText || string(msg) != "ready" {
			t.Fatalf("pairing failed: %v", err)
		}
	}
	tlsConfig, err := relayTLS(code)
	if err != nil {
		t.Fatal(err)
	}
	a := tls.Server(websocket.NetConn(ctx, connections[0], websocket.MessageBinary), tlsConfig)
	b := tls.Client(websocket.NetConn(ctx, connections[1], websocket.MessageBinary), tlsConfig)
	done := make(chan error, 1)
	go func() { done <- a.HandshakeContext(ctx) }()
	if err := b.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	muxCfg := yamux.DefaultConfig()
	muxCfg.LogOutput = io.Discard
	sa, err := yamux.Server(a, muxCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	sb, err := yamux.Client(b, muxCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()
	for _, pair := range [][2]*yamux.Session{{sa, sb}, {sb, sa}} {
		dir := t.TempDir()
		receiver := httptest.NewServer(newReceiverServer(receiverOptions{dir: dir, token: code}).Handler)
		defer receiver.Close()
		go func(session *yamux.Session) {
			stream, err := session.AcceptStream()
			if err == nil {
				relayAccept(stream, strings.TrimPrefix(receiver.URL, "http://"))
			}
		}(pair[1])
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go relayForward(listener, pair[0], 1)
		input := filepath.Join(t.TempDir(), "test.bin")
		payload := bytes.Repeat([]byte("DeskBridge authenticated binary roundtrip\x00\xff"), 25000)
		if err := os.WriteFile(input, payload, 0600); err != nil {
			t.Fatal(err)
		}
		err = sendFile(input, "http://"+listener.Addr().String(), code)
		listener.Close()
		if err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filepath.Join(dir, "test.bin"))
		if err != nil || !bytes.Equal(payload, actual) {
			t.Fatalf("file mismatch: %v", err)
		}
	}
}
