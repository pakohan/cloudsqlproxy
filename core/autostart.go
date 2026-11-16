package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const launchAgentLabel = "com.pakohan.cloudsqlproxy"

// The app starts at login through a per-user LaunchAgent, which works for a
// plain binary as well as an app bundle.
func launchAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist"), nil
}

func AutostartEnabled() bool {
	path, err := launchAgentPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func SetAutostart(enabled bool) error {
	path, err := launchAgentPath()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		log.Println("Removed start at login")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, launchAgentPlist(exe), 0o644); err != nil {
		return err
	}
	log.Printf("Enabled start at login for %s", exe)
	return nil
}

// UpdateAutostart points an existing LaunchAgent at the running binary, in
// case the app was moved or rebuilt elsewhere.
func UpdateAutostart() {
	if !AutostartEnabled() {
		return
	}
	path, _ := launchAgentPath()
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return
	}
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, launchAgentPlist(exe)) {
		return
	}
	if err := SetAutostart(true); err != nil {
		log.Printf("Failed to update start at login: %v", err)
	}
}

func launchAgentPlist(exe string) []byte {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(exe))
	return fmt.Appendf(nil, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`, launchAgentLabel, escaped.String())
}
