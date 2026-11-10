package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recorder struct{ calls []string }

func (r *recorder) run(name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

func TestSystemdUnit(t *testing.T) {
	u := SystemdUnit(Spec{Name: "box", Exe: "/home/me/my tools/runnerkit", Home: "/data/$x"})
	for _, want := range []string{`ExecStart="/home/me/my tools/runnerkit" "supervise" "--home" "/data/$$x" "box"`, "Nice=10", "Restart=always", "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
}

func TestLaunchdPlistEscapes(t *testing.T) {
	p := LaunchdPlist(Spec{Name: "box", Exe: "/Users/me/a&b/runnerkit", Home: "/Users/me/d"})
	if !strings.Contains(p, "a&amp;b") || !strings.Contains(p, "<string>--home</string>") || !strings.Contains(p, "dev.runnerkit.box") || !strings.Contains(p, "<key>ProcessType</key>") {
		t.Fatalf("unexpected plist:\n%s", p)
	}
}

func TestTaskXMLHasNoTimeLimit(t *testing.T) {
	x := TaskXML(Spec{Name: "box", Exe: `C:\Users\me\runnerkit.exe`, Home: `C:\data`})
	for _, want := range []string{"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", "<StopIfGoingOnBatteries>false", `&#34;--home&#34; &#34;C:\data&#34; &#34;box&#34;</Arguments>`, "<LogonTrigger>"} {
		if !strings.Contains(x, want) {
			t.Errorf("task lacks %q", want)
		}
	}
	if b := utf16le("a"); len(b) != 4 || b[0] != 0xFF || b[1] != 0xFE {
		t.Errorf("utf16 encoding wrong: %v", b)
	}
}

func TestLinuxLifecycle(t *testing.T) {
	home := t.TempDir()
	rec := &recorder{}
	m := Manager{GOOS: "linux", Home: home, UID: 1000, Run: rec.run}
	if err := m.Install(Spec{Name: "box", Exe: "/x/runnerkit"}); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", "runnerkit-box.service")
	if _, err := os.Stat(unit); err != nil {
		t.Fatal("unit not written")
	}
	m.Start("box")
	if err := m.Remove("box"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unit); err == nil {
		t.Fatal("unit not removed")
	}
	joined := strings.Join(rec.calls, "\n")
	for _, want := range []string{"systemctl --user daemon-reload", "systemctl --user enable runnerkit-box.service", "systemctl --user start runnerkit-box.service", "systemctl --user stop runnerkit-box.service"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing call %q in:\n%s", want, joined)
		}
	}
}

func TestDarwinAndWindowsCommands(t *testing.T) {
	rec := &recorder{}
	m := Manager{GOOS: "darwin", Home: t.TempDir(), UID: 501, Run: rec.run}
	m.Install(Spec{Name: "box", Exe: "/x"})
	m.Start("box")
	m.Stop("box")
	want := []string{"launchctl bootstrap gui/501", "launchctl bootout gui/501/dev.runnerkit.box"}
	for _, w := range want {
		if !strings.Contains(strings.Join(rec.calls, "\n"), w) {
			t.Errorf("darwin missing %q: %v", w, rec.calls)
		}
	}
	rec = &recorder{}
	w := Manager{GOOS: "windows", Run: rec.run}
	w.Install(Spec{Name: "box", Exe: `C:\x.exe`})
	w.Start("box")
	w.Stop("box")
	w.Remove("box")
	joined := strings.Join(rec.calls, "\n")
	for _, c := range []string{"schtasks /Create /TN runnerkit-box /XML", "schtasks /Run /TN runnerkit-box", "schtasks /End /TN runnerkit-box", "schtasks /Delete /TN runnerkit-box /F"} {
		if !strings.Contains(joined, c) {
			t.Errorf("windows missing %q in:\n%s", c, joined)
		}
	}
}

func TestStatusIsNormalised(t *testing.T) {
	cases := []struct {
		goos, out, want string
	}{
		{"linux", "active\n", "running"},
		{"linux", "inactive\n", "stopped"},
		{"linux", "failed\n", "stopped"},
		{"windows", "Running\r\n", "running"},
		{"windows", "Ready\r\n", "stopped"},
	}
	for _, c := range cases {
		m := Manager{GOOS: c.goos, Run: func(string, ...string) ([]byte, error) { return []byte(c.out), nil }}
		if got := m.Status("box"); got != c.want {
			t.Errorf("%s %q: got %s want %s", c.goos, c.out, got, c.want)
		}
	}
}
