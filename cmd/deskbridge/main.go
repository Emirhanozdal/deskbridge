package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultStateFile = ".deskbridge.json"
	deskflowPort     = 24800
	transferPort     = 47889
	discoveryPort    = 47888
	discoveryMagic   = "deskbridge.v1"
)

type Device struct {
	Name         string `json:"name"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	TransferPort int    `json:"transfer_port"`
}

type Relationship struct {
	Controller string `json:"controller"`
	Target     string `json:"target"`
	Direction  string `json:"direction"`
}

type State struct {
	Devices       map[string]Device `json:"devices"`
	Relationships []Relationship    `json:"relationships"`
	path          string
}

type cli struct {
	statePath string
	stdin     *bufio.Reader
}

type receiverOptions struct {
	clipboardQueue string
	dir            string
	host           string
	port           int
	token          string
	allowBrowserUI bool
}

func main() {
	app := cli{stdin: bufio.NewReader(os.Stdin)}
	os.Exit(app.run(os.Args[1:]))
}

func (c *cli) run(args []string) int {
	global := flag.NewFlagSet("deskbridge", flag.ExitOnError)
	global.StringVar(&c.statePath, "state", getenvDefault("DESKBRIDGE_STATE", defaultStateFile), "state file")
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		rest = []string{"connect"}
	}

	var err error
	switch rest[0] {
	case "connect":
		err = c.cmdConnect(rest[1:])
	case "send-peer":
		err = c.cmdSendPeer(rest[1:])
	case "app":
		err = c.cmdApp()
	case "init":
		err = c.cmdInit()
	case "pair":
		err = c.cmdPair(rest[1:])
	case "list":
		err = c.cmdList()
	case "deskflow-config":
		err = c.cmdDeskflowConfig(rest[1:])
	case "start-server":
		err = c.cmdStartServer(rest[1:])
	case "start-client":
		err = c.cmdStartClient(rest[1:])
	case "receive":
		err = c.cmdConnect(rest[1:])
	case "receive-local":
		err = c.cmdReceive(rest[1:])
	case "tunnel":
		err = c.cmdTunnel(rest[1:])
	case "send":
		err = c.cmdSend(rest[1:])
	case "advertise":
		err = c.cmdAdvertise(rest[1:])
	case "scan":
		err = c.cmdScan(rest[1:])
	case "screen-view":
		err = c.cmdScreenView(rest[1:])
	case "screen-test":
		err = c.cmdScreenTest(rest[1:])
	case "diagnose":
		err = c.cmdDiagnose()
	case "doctor":
		err = c.cmdDiagnose()
	case "help", "-h", "--help":
		usage()
	default:
		err = fmt.Errorf("unknown command: %s", rest[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deskbridge:", err)
		return 1
	}
	return 0
}

func usage() {
	fmt.Println(`DeskBridge

Usage:
  deskbridge [--state .deskbridge.json] <command>

Commands:
  connect             connect both devices through the private 443 relay
  send-peer <file>     send a file to the connected peer
  app                 interactive terminal app
  init                add/update this device
  pair [--scan]       create controller -> target relationship
  list                list devices and relationships
  deskflow-config     print or write Deskflow config
  start-server        start Deskflow server
  start-client        start Deskflow client
  (no command)        connect devices through the private 443 relay
  receive             alias for connect
  receive-local       explicitly receive over local HTTP API
  tunnel              receive files through Cloudflare Tunnel
  send <file>         send a file over HTTP
  advertise <device>  broadcast this device on LAN
  scan                scan LAN broadcasts
  screen-view         open the remote-desktop viewer bridge (spike)
  screen-test         loopback self-test of the remote-desktop video pipe
  diagnose            show local diagnostics
  doctor              alias for diagnose`)
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func loadState(path string) (State, error) {
	state := State{Devices: map[string]Device{}, path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, err
	}
	if state.Devices == nil {
		state.Devices = map[string]Device{}
	}
	state.path = path
	return state, nil
}

func (s State) save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0644)
}

func (s State) sortedNames() []string {
	names := make([]string, 0, len(s.Devices))
	for name := range s.Devices {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *cli) prompt(label, fallback string) string {
	if fallback != "" {
		fmt.Printf("%s [%s]: ", label, fallback)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := c.stdin.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback
	}
	return line
}

func (c *cli) choose(label string, options []string) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("no options for %s", label)
	}
	fmt.Println()
	fmt.Println(label)
	for i, option := range options {
		fmt.Printf("  %d. %s\n", i+1, option)
	}
	for {
		raw := c.prompt("Choose", "1")
		index, err := strconv.Atoi(raw)
		if err == nil && index >= 1 && index <= len(options) {
			return options[index-1], nil
		}
		fmt.Println("Invalid choice.")
	}
}

func (c *cli) cmdInit() error {
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	name := c.prompt("This device name", strings.Split(host, ".")[0])
	reachable := c.prompt("Reachable host/IP for this device", localHostGuess())
	port := parseInt(c.prompt("Deskflow port", strconv.Itoa(deskflowPort)), deskflowPort)
	filePort := parseInt(c.prompt("File transfer port", strconv.Itoa(transferPort)), transferPort)
	state.Devices[name] = Device{Name: name, Host: reachable, Port: port, TransferPort: filePort}
	if err := state.save(); err != nil {
		return err
	}
	fmt.Println("Saved", name, "in", state.path)
	return nil
}

func (c *cli) cmdPair(args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	doScan := fs.Bool("scan", false, "scan before pairing")
	scanSeconds := fs.Int("scan-seconds", 5, "scan seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	if *doScan {
		found, err := scan(*scanSeconds)
		if err != nil {
			return err
		}
		for _, item := range found {
			state.Devices[item.Name] = item
			fmt.Println("Found", item.Name, item.Host)
		}
	}
	for {
		if strings.ToLower(c.prompt("Add/update a device? y/n", "n")) != "y" {
			break
		}
		name := c.prompt("Device name", "")
		host := c.prompt("Reachable host/IP", "")
		port := parseInt(c.prompt("Deskflow port", strconv.Itoa(deskflowPort)), deskflowPort)
		filePort := parseInt(c.prompt("File transfer port", strconv.Itoa(transferPort)), transferPort)
		state.Devices[name] = Device{Name: name, Host: host, Port: port, TransferPort: filePort}
	}
	names := state.sortedNames()
	controller, err := c.choose("Which device owns the keyboard/mouse?", names)
	if err != nil {
		return err
	}
	targets := without(names, controller)
	target, err := c.choose("Which device should be controlled?", targets)
	if err != nil {
		return err
	}
	direction, err := c.choose("Where is the controlled screen?", []string{"left", "right", "up", "down"})
	if err != nil {
		return err
	}
	state.Relationships = upsertRelationship(state.Relationships, Relationship{Controller: controller, Target: target, Direction: direction})
	if err := state.save(); err != nil {
		return err
	}
	fmt.Printf("Paired %s -> %s on %s\n", controller, target, direction)
	return nil
}

func (c *cli) cmdList() error {
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	fmt.Println("Devices")
	for _, name := range state.sortedNames() {
		device := state.Devices[name]
		fmt.Printf("- %s: %s:%d, files :%d\n", device.Name, device.Host, device.Port, device.TransferPort)
	}
	fmt.Println("\nRelationships")
	for _, rel := range state.Relationships {
		fmt.Printf("- %s controls %s at %s\n", rel.Controller, rel.Target, rel.Direction)
	}
	return nil
}

func (c *cli) cmdDeskflowConfig(args []string) error {
	fs := flag.NewFlagSet("deskflow-config", flag.ExitOnError)
	controller := fs.String("controller", "", "controller device")
	write := fs.Bool("write", false, "write config")
	output := fs.String("output", "deskflow.conf", "output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	config, err := renderDeskflowConfig(state, *controller)
	if err != nil {
		return err
	}
	if *write {
		if err := os.WriteFile(*output, []byte(config), 0644); err != nil {
			return err
		}
		fmt.Println("Wrote", *output)
		return nil
	}
	fmt.Print(config)
	return nil
}

func (c *cli) cmdStartServer(args []string) error {
	fs := flag.NewFlagSet("start-server", flag.ExitOnError)
	config := fs.String("config", "deskflow.conf", "Deskflow config")
	name := fs.String("name", "", "this device's name in the screen layout")
	dryRun := fs.Bool("dry-run", false, "print command only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bin := firstDeskflowBinary("server")
	if bin == "" {
		return errors.New("Deskflow server binary was not found in PATH")
	}
	commandArgs := []string{"--config", *config}
	if isModernInputEngine(bin) {
		settings, err := c.coreSettings("server", *name, *config, "", *dryRun)
		if err != nil {
			return err
		}
		commandArgs = []string{"server", "--settings", settings}
	}
	cmd := exec.Command(bin, commandArgs...)
	fmt.Println(strings.Join(cmd.Args, " "))
	if *dryRun {
		return nil
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

func (c *cli) cmdStartClient(args []string) error {
	fs := flag.NewFlagSet("start-client", flag.ExitOnError)
	controller := fs.String("controller", "", "controller device")
	host := fs.String("host", "", "controller host")
	name := fs.String("name", "", "this device's name in the screen layout")
	dryRun := fs.Bool("dry-run", false, "print command only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	targetHost := *host
	if targetHost == "" {
		state, err := loadState(c.statePath)
		if err != nil {
			return err
		}
		name := *controller
		if name == "" {
			name, err = c.choose("Connect to controller", state.sortedNames())
			if err != nil {
				return err
			}
		}
		device, ok := state.Devices[name]
		if !ok {
			return fmt.Errorf("unknown device: %s", name)
		}
		targetHost = device.Host
	}
	bin := firstDeskflowBinary("client")
	if bin == "" {
		return errors.New("Deskflow client binary was not found in PATH")
	}
	commandArgs := []string{targetHost}
	if isModernInputEngine(bin) {
		settings, err := c.coreSettings("client", *name, "", targetHost, *dryRun)
		if err != nil {
			return err
		}
		commandArgs = []string{"client", "--settings", settings}
	}
	cmd := exec.Command(bin, commandArgs...)
	fmt.Println(strings.Join(cmd.Args, " "))
	if *dryRun {
		return nil
	}
	cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
	return cmd.Run()
}

func (c *cli) cmdReceive(args []string) error {
	fs := flag.NewFlagSet("receive", flag.ExitOnError)
	dir := fs.String("dir", defaultDownloadDir(), "destination directory")
	host := fs.String("host", "127.0.0.1", "listen host")
	port := fs.Int("port", transferPort, "listen port")
	token := fs.String("token", getenvDefault("DESKBRIDGE_TOKEN", ""), "upload token")
	allowBrowserUI := fs.Bool("ui", false, "enable browser upload form")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return serveReceiver(receiverOptions{
		dir:            *dir,
		host:           *host,
		port:           *port,
		token:          *token,
		allowBrowserUI: *allowBrowserUI,
	})
}

func (c *cli) cmdTunnel(args []string) error {
	fs := flag.NewFlagSet("tunnel", flag.ExitOnError)
	dir := fs.String("dir", defaultDownloadDir(), "destination directory")
	port := fs.Int("port", transferPort, "local receiver port")
	token := fs.String("token", getenvDefault("DESKBRIDGE_TOKEN", ""), "upload token (generated when omitted)")
	allowBrowserUI := fs.Bool("ui", false, "enable browser upload form")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return err
		}
		*token = hex.EncodeToString(secret)
	}
	if firstBinary("cloudflared") == "" {
		return errors.New("cloudflared was not found in PATH")
	}
	if err := os.MkdirAll(*dir, 0700); err != nil {
		return err
	}
	opts := receiverOptions{dir: *dir, host: "127.0.0.1", port: *port, token: *token, allowBrowserUI: *allowBrowserUI}
	server := newReceiverServer(opts)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	localURL := fmt.Sprintf("http://127.0.0.1:%d", *port)
	fmt.Println("DeskBridge receiver listening locally at", localURL)
	fmt.Println("Starting Cloudflare Tunnel. Use the printed https://*.trycloudflare.com URL with:")
	fmt.Println("  deskbridge send <file> --to <url> --token <token>")
	fmt.Println("Upload token:", *token)
	cmd := exec.Command("cloudflared", "tunnel", "--protocol", "http2", "--url", localURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		_ = server.Shutdown(context.Background())
		return err
	}
	select {
	case err := <-errCh:
		_ = cmd.Process.Kill()
		return err
	case err := <-waitCommand(cmd):
		_ = server.Shutdown(context.Background())
		return err
	}
}

func serveReceiver(opts receiverOptions) error {
	if err := os.MkdirAll(opts.dir, 0755); err != nil {
		return err
	}
	server := newReceiverServer(opts)
	addr := fmt.Sprintf("%s:%d", opts.host, opts.port)
	if opts.token == "" {
		fmt.Println("Warning: receiver upload token is not set. Use --token or DESKBRIDGE_TOKEN before exposing this beyond localhost.")
	}
	fmt.Println("Listening on http://" + addr + " and saving to " + opts.dir)
	return server.ListenAndServe()
}

func newReceiverServer(opts receiverOptions) *http.Server {
	mux := http.NewServeMux()
	registerClipboardReceiver(mux, opts)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if opts.allowBrowserUI {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, renderReceiverHTML(opts.dir, opts.port))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"app":            "deskbridge",
			"dir":            opts.dir,
			"port":           opts.port,
			"auth_required":  opts.token != "",
			"browser_ui":     opts.allowBrowserUI,
			"listen_address": opts.host,
		})
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !authorized(r, opts.token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name, err := saveUpload(r, opts.dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, "saved", name)
	})
	return &http.Server{
		Addr:              fmt.Sprintf("%s:%d", opts.host, opts.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	supplied := r.Header.Get("X-DeskBridge-Token")
	if supplied == "" {
		supplied = r.URL.Query().Get("token")
	}
	if supplied == "" || len(supplied) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(supplied), []byte(token)) == 1
}

func waitCommand(cmd *exec.Cmd) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- cmd.Wait()
	}()
	return ch
}

func (c *cli) cmdSend(args []string) error {
	path, endpoint, token, err := parseSendArgs(args)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("usage: deskbridge send <file> [--to http://host:47889] [--token secret]")
	}
	if endpoint == "" {
		state, err := loadState(c.statePath)
		if err != nil {
			return err
		}
		deviceName, err := c.choose("Send to device", state.sortedNames())
		if err != nil {
			return err
		}
		device := state.Devices[deviceName]
		endpoint = fmt.Sprintf("http://%s:%d", device.Host, device.TransferPort)
	}
	return sendFile(path, endpoint, token)
}

func (c *cli) cmdAdvertise(args []string) error {
	fs := flag.NewFlagSet("advertise", flag.ExitOnError)
	seconds := fs.Int("seconds", 30, "seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: deskbridge advertise <device>")
	}
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	device, ok := state.Devices[fs.Arg(0)]
	if !ok {
		return fmt.Errorf("unknown device: %s", fs.Arg(0))
	}
	fmt.Printf("Advertising %s for %ds\n", device.Name, *seconds)
	return advertise(device, *seconds)
}

func (c *cli) cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	seconds := fs.Int("seconds", 5, "seconds")
	save := fs.Bool("save", false, "save discoveries")
	if err := fs.Parse(args); err != nil {
		return err
	}
	found, err := scan(*seconds)
	if err != nil {
		return err
	}
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	for _, device := range found {
		fmt.Printf("- %s: %s\n", device.Name, device.Host)
		state.Devices[device.Name] = device
	}
	if *save {
		if err := state.save(); err != nil {
			return err
		}
		fmt.Printf("Saved %d device(s) to %s\n", len(found), state.path)
	}
	return nil
}

func (c *cli) cmdDiagnose() error {
	state, err := loadState(c.statePath)
	if err != nil {
		return err
	}
	fmt.Println("State:", state.path)
	fmt.Println("Local host guess:", localHostGuess())
	fmt.Println("server:", missingText(firstDeskflowBinary("server")))
	fmt.Println("client:", missingText(firstDeskflowBinary("client")))
	fmt.Println("gui:", missingText(firstDeskflowBinary("gui")))
	fmt.Println("cloudflared:", missingText(firstBinary("cloudflared")))
	fmt.Println("Devices:", len(state.Devices))
	fmt.Println("Relationships:", len(state.Relationships))
	if firstDeskflowBinary("server") == "" || firstDeskflowBinary("client") == "" {
		fmt.Println("Keyboard/mouse: Deskflow is not installed or not discoverable yet.")
	} else {
		fmt.Println("Keyboard/mouse: ready")
	}
	return nil
}

func (c *cli) cmdApp() error {
	for {
		fmt.Println(`
DeskBridge
  1. Initialize/update this device
  2. Pair devices
  3. List devices and relationships
  4. Generate Deskflow config
  5. Start Deskflow server
  6. Start Deskflow client
  7. Receive files
  8. Send a file
  9. Diagnose
  0. Exit`)
		switch c.prompt("Choose", "1") {
		case "0":
			return nil
		case "1":
			returnIfErr(c.cmdInit())
		case "2":
			returnIfErr(c.cmdPair(nil))
		case "3":
			returnIfErr(c.cmdList())
		case "4":
			returnIfErr(c.cmdDeskflowConfig([]string{"--write"}))
		case "5":
			returnIfErr(c.cmdStartServer(nil))
		case "6":
			returnIfErr(c.cmdStartClient(nil))
		case "7":
			return c.cmdConnect(nil)
		case "8":
			file := c.prompt("File path", "")
			returnIfErr(c.cmdSend([]string{file}))
		case "9":
			returnIfErr(c.cmdDiagnose())
		default:
			fmt.Println("Invalid choice.")
		}
	}
}

func renderReceiverHTML(dir string, port int) string {
	page := strings.ReplaceAll(receiverHTML, "{{DIR}}", html.EscapeString(dir))
	page = strings.ReplaceAll(page, "{{PORT}}", strconv.Itoa(port))
	return page
}

const receiverHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DeskBridge Receiver</title>
  <style>
    body { margin: 0; font-family: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #101418; color: #eef2f3; }
    main { max-width: 720px; margin: 0 auto; padding: 56px 24px; }
    h1 { font-size: 34px; margin: 0 0 10px; letter-spacing: 0; }
    p { color: #b7c0c7; line-height: 1.5; }
    form { margin-top: 28px; }
    .dropzone { display: grid; place-items: center; min-height: 220px; border: 2px dashed #3b4a55; padding: 24px; background: #171d22; border-radius: 8px; text-align: center; transition: border-color .12s, background .12s; }
    .dropzone.dragging { border-color: #18a999; background: #132420; }
    input[type=file] { position: absolute; inline-size: 1px; block-size: 1px; opacity: 0; pointer-events: none; }
    input[type=text] { display: block; width: 100%; box-sizing: border-box; margin: 16px 0; padding: 11px 12px; border: 1px solid #2a333b; border-radius: 6px; background: #0f1418; color: #eef2f3; }
    button { background: #18a999; border: 0; color: #061311; padding: 12px 16px; border-radius: 6px; font-weight: 700; cursor: pointer; }
    code { color: #91d7ff; }
    #status { min-height: 24px; margin-top: 16px; }
  </style>
</head>
<body>
  <main>
    <h1>DeskBridge Receiver</h1>
    <p>Receiving into <code>{{DIR}}</code> on port <code>{{PORT}}</code>.</p>
    <form id="uploadForm">
      <label class="dropzone" id="dropzone">
        <span><strong>Drop a file here</strong><br>or click to choose one</span>
        <input id="fileInput" type="file" name="file" required>
      </label>
      <input id="tokenInput" type="text" name="token" placeholder="Token, if receiver requires one">
      <button type="submit">Upload File</button>
    </form>
    <p id="status"></p>
  </main>
  <script>
    const form = document.getElementById('uploadForm');
    const dropzone = document.getElementById('dropzone');
    const input = document.getElementById('fileInput');
    const token = document.getElementById('tokenInput');
    const status = document.getElementById('status');

    function setFile(file) {
      const dt = new DataTransfer();
      dt.items.add(file);
      input.files = dt.files;
      status.textContent = file.name + ' ready';
    }

    ['dragenter', 'dragover'].forEach(name => {
      dropzone.addEventListener(name, event => {
        event.preventDefault();
        dropzone.classList.add('dragging');
      });
    });
    ['dragleave', 'drop'].forEach(name => {
      dropzone.addEventListener(name, event => {
        event.preventDefault();
        dropzone.classList.remove('dragging');
      });
    });
    dropzone.addEventListener('drop', event => {
      const file = event.dataTransfer.files[0];
      if (file) setFile(file);
    });
    input.addEventListener('change', () => {
      if (input.files[0]) status.textContent = input.files[0].name + ' ready';
    });
    form.addEventListener('submit', async event => {
      event.preventDefault();
      if (!input.files[0]) return;
      const body = new FormData();
      body.append('file', input.files[0]);
      const headers = {};
      if (token.value.trim()) headers['X-DeskBridge-Token'] = token.value.trim();
      status.textContent = 'Uploading...';
      const response = await fetch('/upload', { method: 'POST', headers, body });
      status.textContent = response.ok ? await response.text() : 'Upload failed: ' + response.status;
      if (response.ok) input.value = '';
    });
  </script>
</body>
</html>`

func returnIfErr(err error) {
	if err != nil {
		fmt.Println("Error:", err)
	}
}

func parseInt(value string, fallback int) int {
	number, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return number
}

func parseSendArgs(args []string) (string, string, string, error) {
	var path string
	var endpoint string
	token := getenvDefault("DESKBRIDGE_TOKEN", "")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--to":
			i++
			if i >= len(args) {
				return "", "", "", errors.New("--to needs a value")
			}
			endpoint = args[i]
		case strings.HasPrefix(arg, "--to="):
			endpoint = strings.TrimPrefix(arg, "--to=")
		case arg == "--token":
			i++
			if i >= len(args) {
				return "", "", "", errors.New("--token needs a value")
			}
			token = args[i]
		case strings.HasPrefix(arg, "--token="):
			token = strings.TrimPrefix(arg, "--token=")
		case strings.HasPrefix(arg, "-"):
			return "", "", "", fmt.Errorf("unknown send option: %s", arg)
		case path == "":
			path = arg
		default:
			return "", "", "", fmt.Errorf("unexpected send argument: %s", arg)
		}
	}
	return path, endpoint, token, nil
}

func without(items []string, item string) []string {
	out := []string{}
	for _, current := range items {
		if current != item {
			out = append(out, current)
		}
	}
	return out
}

func upsertRelationship(items []Relationship, rel Relationship) []Relationship {
	out := []Relationship{}
	for _, current := range items {
		if current.Controller == rel.Controller && current.Target == rel.Target {
			continue
		}
		out = append(out, current)
	}
	return append(out, rel)
}

func renderDeskflowConfig(state State, controller string) (string, error) {
	rels := state.Relationships
	if controller != "" {
		filtered := []Relationship{}
		for _, rel := range rels {
			if rel.Controller == controller {
				filtered = append(filtered, rel)
			}
		}
		rels = filtered
	}
	if len(rels) == 0 {
		return "", errors.New("no relationships configured")
	}
	screenSet := map[string]bool{}
	for _, rel := range rels {
		screenSet[rel.Controller] = true
		screenSet[rel.Target] = true
	}
	screens := make([]string, 0, len(screenSet))
	for screen := range screenSet {
		screens = append(screens, screen)
	}
	sort.Strings(screens)

	var b strings.Builder
	b.WriteString("section: screens\n")
	for _, screen := range screens {
		fmt.Fprintf(&b, "\t%s:\n", screen)
	}
	b.WriteString("end\n\nsection: links\n")
	for _, rel := range rels {
		fmt.Fprintf(&b, "\t%s:\n\t\t%s = %s\n", rel.Controller, rel.Direction, rel.Target)
		fmt.Fprintf(&b, "\t%s:\n\t\t%s = %s\n", rel.Target, opposite(rel.Direction), rel.Controller)
	}
	b.WriteString("end\n\nsection: aliases\n")
	for _, screen := range screens {
		device, ok := state.Devices[screen]
		if ok {
			fmt.Fprintf(&b, "\t%s:\n\t\t%s\n", screen, device.Host)
		}
	}
	b.WriteString("end\n")
	return b.String(), nil
}

func opposite(direction string) string {
	switch direction {
	case "left":
		return "right"
	case "right":
		return "left"
	case "up":
		return "down"
	case "down":
		return "up"
	default:
		return "left"
	}
}

func firstBinary(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func firstDeskflowBinary(kind string) string {
	if kind == "server" || kind == "client" {
		if input := firstExisting(os.Getenv("DESKBRIDGE_INPUT_BIN"), firstBinary("deskbridge-input")); input != "" {
			return input
		}
	}
	switch kind {
	case "server":
		return firstExisting(
			firstBinary("deskflow-server", "deskflow-core", "synergys", "input-leaps"),
			"/Applications/Deskflow.app/Contents/MacOS/deskflow-core",
			"/Applications/Deskflow.app/Contents/MacOS/deskflow-server",
			"/Applications/Input Leap.app/Contents/MacOS/input-leaps",
		)
	case "client":
		return firstExisting(
			firstBinary("deskflow-client", "deskflow-core", "synergyc", "input-leapc"),
			"/Applications/Deskflow.app/Contents/MacOS/deskflow-core",
			"/Applications/Deskflow.app/Contents/MacOS/deskflow-client",
			"/Applications/Input Leap.app/Contents/MacOS/input-leapc",
		)
	case "gui":
		return firstExisting(
			firstBinary("deskflow", "input-leap", "barrier"),
			"/Applications/Deskflow.app/Contents/MacOS/Deskflow",
			"/Applications/Deskflow.app/Contents/MacOS/deskflow",
			"/Applications/Input Leap.app/Contents/MacOS/Input Leap",
		)
	default:
		return ""
	}
}

func isModernInputEngine(bin string) bool {
	return filepath.Base(bin) == "deskflow-core" || filepath.Base(bin) == "deskbridge-input"
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func missingText(value string) string {
	if value == "" {
		return "missing"
	}
	return value
}

func localHostGuess() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "linux" {
		if config, err := os.UserConfigDir(); err == nil {
			if data, err := os.ReadFile(filepath.Join(config, "user-dirs.dirs")); err == nil {
				if dir := xdgDownloadDir(string(data), home); dir != "" {
					return dir
				}
			}
		}
	}
	return filepath.Join(home, "Downloads")
}

func xdgDownloadDir(config, home string) string {
	for _, line := range strings.Split(config, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "XDG_DOWNLOAD_DIR" {
			continue
		}
		dir, err := strconv.Unquote(strings.TrimSpace(value))
		if err != nil {
			return ""
		}
		if dir == "$HOME" {
			dir = home
		} else if strings.HasPrefix(dir, "$HOME/") {
			dir = filepath.Join(home, strings.TrimPrefix(dir, "$HOME/"))
		}
		if filepath.IsAbs(dir) && !strings.Contains(dir, "$") {
			return filepath.Clean(dir)
		}
	}
	return ""
}

func sendFile(path, endpoint, token string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	uploadURL := strings.TrimRight(endpoint, "/") + "/upload?name=" + url.QueryEscape(filepath.Base(path))
	req, err := http.NewRequest(http.MethodPost, uploadURL, file)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", mime.TypeByExtension(filepath.Ext(path)))
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	if token != "" {
		req.Header.Set("X-DeskBridge-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("transfer failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	fmt.Println(strings.TrimSpace(string(body)))
	return nil
}

func saveUpload(r *http.Request, dir string) (string, error) {
	contentType := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "multipart/form-data" {
		return saveMultipartUpload(r, dir)
	}
	name := cleanUploadName(r.URL.Query().Get("name"))
	return saveUniqueUpload(r.Body, dir, name)
}

func saveMultipartUpload(r *http.Request, dir string) (string, error) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return "", err
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := firstMultipartFile(r.MultipartForm)
	if err != nil {
		return "", err
	}
	defer file.Close()
	name := cleanUploadName(header.Filename)
	return saveUniqueUpload(file, dir, name)
}

func firstMultipartFile(form *multipart.Form) (multipart.File, *multipart.FileHeader, error) {
	if form == nil {
		return nil, nil, errors.New("missing multipart form")
	}
	for _, headers := range form.File {
		if len(headers) == 0 {
			continue
		}
		file, err := headers[0].Open()
		return file, headers[0], err
	}
	return nil, nil, errors.New("multipart form did not include a file")
}

func cleanUploadName(name string) string {
	clean := filepath.Base(name)
	if clean == "." || clean == string(filepath.Separator) || clean == "" {
		return "deskbridge-upload.bin"
	}
	return clean
}

func advertise(device Device, seconds int) error {
	payload, err := json.Marshal(map[string]any{"magic": discoveryMagic, "device": device})
	if err != nil {
		return err
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", discoveryPort))
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		if _, err := conn.Write(payload); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
	return nil
}

func scan(seconds int) ([]Device, error) {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", discoveryPort))
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	found := map[string]Device{}
	buf := make([]byte, 65535)
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			continue
		}
		if err != nil {
			return nil, err
		}
		var msg struct {
			Magic  string `json:"magic"`
			Device Device `json:"device"`
		}
		if err := json.NewDecoder(bytes.NewReader(buf[:n])).Decode(&msg); err != nil {
			continue
		}
		if msg.Magic == discoveryMagic && msg.Device.Name != "" {
			found[msg.Device.Name] = msg.Device
		}
	}
	devices := make([]Device, 0, len(found))
	for _, device := range found {
		devices = append(devices, device)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })
	return devices, nil
}
