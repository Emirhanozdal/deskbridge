package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreSettings(t *testing.T) {
	dir := t.TempDir()
	c := cli{statePath: filepath.Join(dir, "state.json")}
	config := filepath.Join(dir, "screen layout.conf")
	if err := os.WriteFile(config, []byte("section: screens\n imac:\nend\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := c.coreSettings("server", "imac", config, "", false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"computerName=\"imac\"", "externalConfig=true", "externalConfigFile=" + iniValue(config), "tlsEnabled=true", "checkPeerFingerprints=true"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %s", want)
		}
	}
	if _, err := c.coreSettings("client", "evil\n[security]", "", "localhost", false); err == nil {
		t.Fatal("accepted injected setting")
	}
	if _, err := c.coreSettings("server", "imac", filepath.Join(dir, "missing"), "", false); err == nil {
		t.Fatal("accepted missing layout")
	}
}
