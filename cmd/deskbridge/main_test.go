package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	state := State{
		Devices: map[string]Device{
			"imac":     {Name: "imac", Host: "100.64.0.10", Port: 24800, TransferPort: 47889},
			"linux-pc": {Name: "linux-pc", Host: "100.64.0.20", Port: 24800, TransferPort: 47889},
		},
		Relationships: []Relationship{{Controller: "imac", Target: "linux-pc", Direction: "right"}},
		path:          path,
	}
	if err := state.save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Devices["imac"].Host != "100.64.0.10" {
		t.Fatalf("unexpected host: %s", loaded.Devices["imac"].Host)
	}
}

func TestRenderDeskflowConfig(t *testing.T) {
	state := State{
		Devices: map[string]Device{
			"imac":     {Name: "imac", Host: "imac.tailnet"},
			"linux-pc": {Name: "linux-pc", Host: "linux.tailnet"},
		},
		Relationships: []Relationship{{Controller: "imac", Target: "linux-pc", Direction: "right"}},
	}
	config, err := renderDeskflowConfig(state, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"imac:", "right = linux-pc", "left = imac", "linux.tailnet"} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
}

func TestParseSendArgsAllowsOptionsAfterPath(t *testing.T) {
	path, endpoint, token, err := parseSendArgs([]string{"file.txt", "--to", "http://127.0.0.1:47889", "--token", "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "file.txt" || endpoint != "http://127.0.0.1:47889" || token != "secret" {
		t.Fatalf("unexpected parse result: %q %q %q", path, endpoint, token)
	}
}

func TestCleanUploadName(t *testing.T) {
	if got := cleanUploadName("../../secret.txt"); got != "secret.txt" {
		t.Fatalf("unexpected clean name: %s", got)
	}
	if got := cleanUploadName(""); got != "deskbridge-upload.bin" {
		t.Fatalf("unexpected fallback name: %s", got)
	}
}

func TestAuthorized(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "/upload", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-DeskBridge-Token", "secret")
	if !authorized(req, "secret") {
		t.Fatal("expected request to be authorized")
	}
	if authorized(req, "other") {
		t.Fatal("expected request to be rejected")
	}
}
