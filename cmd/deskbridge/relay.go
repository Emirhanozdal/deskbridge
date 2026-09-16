package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

type relaySettings struct {
	URL  string `json:"url"`
	Code string `json:"code"`
	Side string `json:"side"`
}

func relaySettingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "deskbridge", "relay.json"), err
}

func (c *cli) cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	endpoint := fs.String("relay", "", "HTTPS relay URL (saved after first connection)")
	code := fs.String("code", "", "private pairing code")
	side := fs.String("side", "", "a on Mac, b on Linux")
	dir := fs.String("dir", defaultDownloadDir(), "incoming files directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path, err := relaySettingsPath()
	if err != nil {
		return err
	}
	var cfg relaySettings
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if *endpoint != "" {
		cfg.URL = *endpoint
	}
	if *code != "" {
		cfg.Code = *code
	}
	if *side != "" {
		cfg.Side = *side
	}
	if cfg.URL == "" {
		cfg.URL = "https://deskbridge-relay.emirhanozdall.workers.dev"
	}
	if cfg.Code == "" {
		cfg.Code = c.prompt("Private pairing code", "")
	}
	if cfg.Side == "" {
		cfg.Side = c.prompt("Device side: a (Mac), b (Linux)", "a")
	}
	cfg.Code = strings.ToLower(strings.TrimSpace(cfg.Code))
	if err := validateRelay(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		err := runRelay(ctx, cfg, *dir)
		if ctx.Err() != nil {
			return nil
		}
		fmt.Fprintln(os.Stderr, "Connection ended:", err, "- retrying in 5 seconds")
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

func validateRelay(cfg relaySettings) error {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("relay must be an HTTPS origin")
	}
	decoded, err := hex.DecodeString(cfg.Code)
	if err != nil || len(decoded) != 32 {
		return errors.New("pairing code must contain 64 hexadecimal characters")
	}
	if cfg.Side != "a" && cfg.Side != "b" {
		return errors.New("side must be a or b")
	}
	return nil
}

// The relay receives only a one-way hash, never the secret used for inner TLS.
func relayAuth(code string) string {
	sum := sha256.Sum256([]byte("deskbridge-auth:" + code))
	return hex.EncodeToString(sum[:])
}

func relayTLS(code string) (*tls.Config, error) {
	seed := sha256.Sum256([]byte("deskbridge-tls:" + code))
	key := ed25519.NewKeyFromSeed(seed[:])
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "deskbridge"}, DNSNames: []string{"deskbridge"},
		NotBefore: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:        true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(nil, template, template, key.Public(), key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "deskbridge", RootCAs: roots, ClientCAs: roots,
		ClientAuth: tls.RequireAndVerifyClientCert, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, nil
}

func runRelay(ctx context.Context, cfg relaySettings, dir string) error {
	u, _ := url.Parse(cfg.URL)
	u.Scheme = "wss"
	u.Path = "/connect/" + cfg.Side
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	ws, _, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + relayAuth(cfg.Code)}}})
	cancel()
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1048576)
	fmt.Println("Connected to relay over 443. Waiting for the other device...")
	typ, message, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	if typ != websocket.MessageText || string(message) != "ready" {
		return errors.New("unexpected relay response")
	}
	tlsCfg, err := relayTLS(cfg.Code)
	if err != nil {
		return err
	}
	raw := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	var secured *tls.Conn
	if cfg.Side == "a" {
		secured = tls.Server(raw, tlsCfg)
	} else {
		secured = tls.Client(raw, tlsCfg)
	}
	defer secured.Close()
	handshakeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = secured.HandshakeContext(handshakeCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("peer authentication: %w", err)
	}
	muxCfg := yamux.DefaultConfig()
	muxCfg.LogOutput = io.Discard
	var session *yamux.Session
	if cfg.Side == "a" {
		session, err = yamux.Server(secured, muxCfg)
	} else {
		session, err = yamux.Client(secured, muxCfg)
	}
	if err != nil {
		return err
	}
	defer session.Close()
	configPath, err := relaySettingsPath()
	if err != nil {
		return err
	}
	receiver := newReceiverServer(receiverOptions{dir: dir, token: cfg.Code, host: "127.0.0.1", clipboardQueue: filepath.Join(filepath.Dir(configPath), "clipboard-inbox")})
	incoming, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer incoming.Close()
	defer receiver.Close()
	go receiver.Serve(incoming)
	files, err := net.Listen("tcp", "127.0.0.1:47890")
	if err != nil {
		return err
	}
	defer files.Close()
	kvm, err := net.Listen("tcp", "127.0.0.1:24801")
	if err != nil {
		return err
	}
	defer kvm.Close()
	// Remote-desktop spike (additive, see screen.go). The viewer side exposes two
	// extra loopback ports: screen (framed MJPEG in) and control (framed input
	// events out). Both open a yamux stream and write [service, role]; the peer
	// answers service 3 by pushing frames and service 4 by injecting input.
	screen, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", screenViewPort))
	if err != nil {
		return err
	}
	defer screen.Close()
	control, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", controlInputPort))
	if err != nil {
		return err
	}
	defer control.Close()
	go relayForward(files, session, 1)
	go relayForward(kvm, session, 2)
	go relayForwardHeader(screen, session, screenService, roleViewer)
	go relayForwardHeader(control, session, controlService, roleViewer)
	go func() {
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return
			}
			go relayAccept(stream, incoming.Addr().String())
		}
	}()
	fmt.Println("Peer authenticated. End-to-end encrypted connection ready.")
	fmt.Println("Files: deskbridge send-peer <file> | Remote Deskflow: 127.0.0.1:24801")
	fmt.Printf("Remote desktop (spike): screen 127.0.0.1:%d | control 127.0.0.1:%d\n", screenViewPort, controlInputPort)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-session.CloseChan():
		return errors.New("peer disconnected")
	}
}

func relayForward(listener net.Listener, session *yamux.Session, service byte) {
	relayForwardHeader(listener, session, service)
}

// relayForwardHeader is the generalized forwarder: it writes an arbitrary header
// (service id, optionally followed by a role/direction byte) at the head of each
// new yamux stream, then pipes bytes both ways. Services 1/2 pass a single byte
// (identical to the original behavior); services 3/4 pass [service, role].
func relayForwardHeader(listener net.Listener, session *yamux.Session, header ...byte) {
	head := append([]byte(nil), header...)
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer local.Close()
			remote, err := session.OpenStream()
			if err != nil {
				return
			}
			defer remote.Close()
			if _, err := remote.Write(head); err != nil {
				return
			}
			copyRelay(local, remote)
		}()
	}
}

func relayAccept(stream *yamux.Stream, fileAddress string) {
	defer stream.Close()
	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	var service [1]byte
	if _, err := io.ReadFull(stream, service[:]); err != nil {
		return
	}
	// Remote-desktop services carry a second header byte (role/direction) and are
	// not simple local-TCP dials, so they branch out before the dial path below.
	if service[0] == screenService || service[0] == controlService {
		var role [1]byte
		if _, err := io.ReadFull(stream, role[:]); err != nil {
			return
		}
		_ = stream.SetReadDeadline(time.Time{})
		if service[0] == screenService {
			// Peer requested our screen: capture and push framed MJPEG until the
			// stream closes. Bound to the stream's lifetime.
			_ = pushScreen(context.Background(), stream, defaultCaptureOptions())
		} else {
			// Peer is sending absolute input events for us to inject.
			_ = serveControlSink(stream)
		}
		return
	}
	_ = stream.SetReadDeadline(time.Time{})
	address := fileAddress
	if service[0] == 2 {
		address = "127.0.0.1:24800"
	} else if service[0] != 1 {
		return
	}
	local, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return
	}
	defer local.Close()
	copyRelay(local, stream)
}

func copyRelay(a, b net.Conn) {
	done := make(chan struct{})
	go func() { _, _ = io.Copy(a, b); _ = a.Close(); _ = b.Close(); close(done) }()
	_, _ = io.Copy(b, a)
	_ = a.Close()
	_ = b.Close()
	<-done
}

func (c *cli) cmdSendPeer(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: deskbridge send-peer <file>")
	}
	path, err := relaySettingsPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("run deskbridge connect first")
	}
	var cfg relaySettings
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	return sendFile(args[0], "http://127.0.0.1:47890", strings.TrimSpace(cfg.Code))
}
