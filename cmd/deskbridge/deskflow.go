package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Modern Deskflow reads INI settings separately from its screen layout.
func (c *cli) coreSettings(mode, name, config, host string, dryRun bool) (string, error) {
	if name == "" {
		var err error
		name, err = os.Hostname()
		if err != nil {
			return "", err
		}
	}
	for _, value := range []string{name, config, host} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return "", fmt.Errorf("invalid multiline Deskflow setting")
		}
	}
	settings, err := filepath.Abs(filepath.Join(filepath.Dir(c.statePath), "deskbridge-"+mode+".ini"))
	if err != nil {
		return "", err
	}
	content := fmt.Sprintf("[core]\ncomputerName=%s\n\n[security]\ntlsEnabled=true\ncheckPeerFingerprints=true\n\n", iniValue(name))
	if mode == "server" {
		absolute, err := filepath.Abs(config)
		if err != nil {
			return "", err
		}
		if !dryRun {
			if _, err := os.Stat(absolute); err != nil {
				return "", fmt.Errorf("screen layout: %w; run deskbridge deskflow-config --write first", err)
			}
		}
		content += "[server]\nexternalConfig=true\nexternalConfigFile=" + iniValue(absolute) + "\n"
	} else {
		content += "[client]\nremoteHost=" + iniValue(host) + "\n"
	}
	if !dryRun {
		if err := os.WriteFile(settings, []byte(content), 0600); err != nil {
			return "", err
		}
	}
	return settings, nil
}

func iniValue(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}
