package service

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

type Spec struct {
	Name string
	Exe  string
	Home string
	User string
}

func (s Spec) args() []string {
	return []string{"supervise", "--home", s.Home, s.Name}
}

type Manager struct {
	GOOS string
	Home string
	UID  int
	Run  func(name string, args ...string) ([]byte, error)
}

func TaskName(name string) string { return "runnerkit-" + name }

func UnitName(name string) string { return "runnerkit-" + name + ".service" }

func LaunchdLabel(name string) string { return "dev.runnerkit." + name }

func (m Manager) unitPath(name string) string {
	switch m.GOOS {
	case "linux":
		return filepath.Join(m.Home, ".config", "systemd", "user", UnitName(name))
	case "darwin":
		return filepath.Join(m.Home, "Library", "LaunchAgents", LaunchdLabel(name)+".plist")
	}
	return ""
}

func SystemdUnit(s Spec) string {
	return fmt.Sprintf(`[Unit]
Description=runnerkit runner %s
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s
Restart=always
RestartSec=10
Nice=10
IOSchedulingClass=idle
KillMode=mixed
TimeoutStopSec=1h

[Install]
WantedBy=default.target
`, s.Name, systemdCommand(s))
}

func systemdCommand(s Spec) string {
	parts := []string{systemdQuote(s.Exe)}
	for _, a := range s.args() {
		parts = append(parts, systemdQuote(a))
	}
	return strings.Join(parts, " ")
}

func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
}

func LaunchdPlist(s Spec) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>%s
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>ProcessType</key>
  <string>Background</string>
  <key>Nice</key>
  <integer>10</integer>
  <key>LowPriorityIO</key>
  <true/>
</dict>
</plist>
`, LaunchdLabel(s.Name), html.EscapeString(s.Exe), plistArgs(s))
}

func plistArgs(s Spec) string {
	var b strings.Builder
	for _, a := range s.args() {
		fmt.Fprintf(&b, "\n    <string>%s</string>", html.EscapeString(a))
	}
	return b.String()
}

func windowsArgs(s Spec) string {
	parts := make([]string, 0, 4)
	for _, a := range s.args() {
		parts = append(parts, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
	}
	return strings.Join(parts, " ")
}

func TaskXML(s Spec) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>runnerkit runner %s</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>%s
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">%s
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, html.EscapeString(s.Name), taskUser(s, "\n      "), taskUser(s, "\n      "), html.EscapeString(s.Exe), html.EscapeString(windowsArgs(s)))
}

func taskUser(s Spec, indent string) string {
	if s.User == "" {
		return ""
	}
	return indent + "<UserId>" + html.EscapeString(s.User) + "</UserId>"
}

func utf16le(s string) []byte {
	units := utf16.Encode([]rune(s))
	buf := bytes.NewBuffer([]byte{0xFF, 0xFE})
	for _, u := range units {
		buf.WriteByte(byte(u))
		buf.WriteByte(byte(u >> 8))
	}
	return buf.Bytes()
}

func (m Manager) run(name string, args ...string) error {
	out, err := m.Run(name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m Manager) Install(s Spec) error {
	switch m.GOOS {
	case "linux":
		path := m.unitPath(s.Name)
		if err := writeUnit(path, []byte(SystemdUnit(s))); err != nil {
			return err
		}
		if err := m.run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		return m.run("systemctl", "--user", "enable", UnitName(s.Name))
	case "darwin":
		return writeUnit(m.unitPath(s.Name), []byte(LaunchdPlist(s)))
	case "windows":
		tmp, err := os.CreateTemp("", "runnerkit-*.xml")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		_, err = tmp.Write(utf16le(TaskXML(s)))
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		return m.run("schtasks", "/Create", "/TN", TaskName(s.Name), "/XML", tmp.Name(), "/F")
	}
	return fmt.Errorf("services are not supported on %s", m.GOOS)
}

func writeUnit(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (m Manager) Start(name string) error {
	switch m.GOOS {
	case "linux":
		return m.run("systemctl", "--user", "start", UnitName(name))
	case "darwin":
		domain := fmt.Sprintf("gui/%d", m.UID)
		if _, err := m.Run("launchctl", "print", domain); err != nil {
			return fmt.Errorf("there is no graphical login session for this user, so launchd cannot start the runner yet; log in on the Mac screen once, then run `runnerkit start %s`", name)
		}
		if err := m.run("launchctl", "bootstrap", domain, m.unitPath(name)); err != nil {
			if kerr := m.run("launchctl", "kickstart", domain+"/"+LaunchdLabel(name)); kerr != nil {
				return err
			}
		}
		return nil
	case "windows":
		return m.run("schtasks", "/Run", "/TN", TaskName(name))
	}
	return fmt.Errorf("services are not supported on %s", m.GOOS)
}

func (m Manager) Stop(name string) error {
	switch m.GOOS {
	case "linux":
		return m.run("systemctl", "--user", "stop", UnitName(name))
	case "darwin":
		return m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", m.UID, LaunchdLabel(name)))
	case "windows":
		return m.run("schtasks", "/End", "/TN", TaskName(name))
	}
	return fmt.Errorf("services are not supported on %s", m.GOOS)
}

func (m Manager) StopNow(name string) error {
	if m.GOOS == "linux" {
		_ = m.run("systemctl", "--user", "kill", "--signal=SIGKILL", UnitName(name))
	}
	return m.Stop(name)
}

func (m Manager) Remove(name string) error {
	_ = m.Stop(name)
	switch m.GOOS {
	case "linux":
		_ = m.run("systemctl", "--user", "disable", UnitName(name))
		if err := os.Remove(m.unitPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return m.run("systemctl", "--user", "daemon-reload")
	case "darwin":
		if err := os.Remove(m.unitPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	case "windows":
		return m.run("schtasks", "/Delete", "/TN", TaskName(name), "/F")
	}
	return fmt.Errorf("services are not supported on %s", m.GOOS)
}

func (m Manager) Status(name string) string {
	switch m.GOOS {
	case "linux":
		out, _ := m.Run("systemctl", "--user", "is-active", UnitName(name))
		switch firstWord(string(out), "unknown") {
		case "active", "activating", "reloading":
			return "running"
		case "inactive", "failed", "deactivating":
			return "stopped"
		}
	case "darwin":
		out, err := m.Run("launchctl", "print", fmt.Sprintf("gui/%d/%s", m.UID, LaunchdLabel(name)))
		if err != nil {
			return "stopped"
		}
		if strings.Contains(string(out), "state = running") {
			return "running"
		}
		return "stopped"
	case "windows":
		out, err := m.Run("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf("(Get-ScheduledTask -TaskName '%s').State", TaskName(name)))
		if err != nil {
			return "missing"
		}
		if strings.EqualFold(firstWord(string(out), ""), "Running") {
			return "running"
		}
		return "stopped"
	}
	return "unknown"
}

func firstWord(s, fallback string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return fallback
	}
	return fields[0]
}
