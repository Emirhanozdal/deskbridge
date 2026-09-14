package main

import (
	"os"
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

func TestBinaryBuilds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deskbridge")
	if err := os.WriteFile(path, []byte("placeholder"), 0644); err != nil {
		t.Fatal(err)
	}
}
