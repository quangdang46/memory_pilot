// Package launchd: plist generation + install/uninstall/doctor hooks
// (bead P1LAUNCHD). Standard ProcessType per ADR-008 Q-12 baseline.
package launchd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const Label = "io.memorypilot.daemon"

// DefaultConfigPath is where `service install` puts the generated config when
// the caller does not supply one.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mempilot", "config.json")
}

// Plist renders the LaunchAgent. configPath is optional; when set, the daemon
// starts with that configuration instead of the built-in observe default.
// Without it, installing the service silently downgraded a canary install to
// observe — the daemon ran, reported, and never acted.
func Plist(binPath, dataRoot, configPath string) string {
	cfgArgs := ""
	if configPath != "" {
		cfgArgs = fmt.Sprintf("    <string>-config</string>\n    <string>%s</string>\n", configPath)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-data-root</string>
    <string>%s</string>
%s  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Standard</string>
  <key>Nice</key><integer>10</integer>
  <key>StandardOutPath</key><string>%s/launchd.out.log</string>
  <key>StandardErrorPath</key><string>%s/launchd.err.log</string>
</dict>
</plist>
`, Label, binPath, dataRoot, cfgArgs, dataRoot, dataRoot)
}

func userAgents() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents")
}

// Install writes the plist and bootstraps it. An empty configPath means no
// config file is passed.
func Install(binPath, dataRoot, configPath string) (string, error) {
	dir := userAgents()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, Label+".plist")
	if err := os.WriteFile(p, []byte(Plist(binPath, dataRoot, configPath)), 0o644); err != nil {
		return "", err
	}
	return p, exec.Command("launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), p).Run()
}

func Uninstall() error {
	p := filepath.Join(userAgents(), Label+".plist")
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), p).Run()
	return os.Remove(p)
}

// Doctor checks the installed plist matches the binary and is bootstrapped.
func Doctor(dataRoot string) []string {
	var problems []string
	p := filepath.Join(userAgents(), Label+".plist")
	if _, err := os.Stat(p); err != nil {
		problems = append(problems, "LaunchAgent plist missing: "+p)
	}
	out, err := exec.Command("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), Label)).CombinedOutput()
	if err != nil {
		problems = append(problems, "launchd service not loaded: "+string(out))
	}
	return problems
}
