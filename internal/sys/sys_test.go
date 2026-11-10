package sys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsers(t *testing.T) {
	if !PmsetOnBattery("Now drawing from 'Battery Power'\n -InternalBattery-0 80%") {
		t.Error("pmset on battery")
	}
	if PmsetOnBattery("Now drawing from 'AC Power'") {
		t.Error("pmset on AC")
	}
	if !WindowsOnBattery("1\r\n") || WindowsOnBattery("2\r\n") || WindowsOnBattery("") {
		t.Error("windows battery status")
	}
}

func supply(t *testing.T, dir, name, kind, key, value string) {
	p := filepath.Join(dir, name)
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "type"), []byte(kind+"\n"), 0o644)
	os.WriteFile(filepath.Join(p, key), []byte(value+"\n"), 0o644)
}

func TestLinuxOnBattery(t *testing.T) {
	dir := t.TempDir()
	if linuxOnBattery(dir) {
		t.Error("no supplies means a desktop")
	}
	supply(t, dir, "BAT0", "Battery", "status", "Discharging")
	supply(t, dir, "AC", "Mains", "online", "0")
	if !linuxOnBattery(dir) {
		t.Error("discharging with mains offline")
	}
	supply(t, dir, "AC", "Mains", "online", "1")
	if linuxOnBattery(dir) {
		t.Error("mains online wins")
	}
}

func TestWorkerBelow(t *testing.T) {
	ps := `  1     0 systemd
 100     1 runnerkit
 200   100 run.sh
 201   200 run-helper.sh
 202   201 Runner.Listener
 203   202 Runner.Worker
 300     1 Runner.Worker
`
	if !WorkerBelow(ps, 200) {
		t.Error("worker is below 200")
	}
	if WorkerBelow(ps, 201) != true {
		t.Error("worker is below 201")
	}
	if WorkerBelow(ps, 203) {
		t.Error("the worker has no worker below it")
	}
	idle := "  200   100 run.sh\n  202   200 Runner.Listener\n  300     1 Runner.Worker\n"
	if WorkerBelow(idle, 200) {
		t.Error("a worker of another tree must not count")
	}
	if !WorkerBelow(idle, 0) {
		t.Error("pid 0 means anywhere on the machine")
	}
	mac := " 202   200 /Users/me/runnerkit/runners/a/bin/Runner.Worker\n"
	if !WorkerBelow(mac, 200) {
		t.Error("macOS prints full paths")
	}
}

func TestWindowsWorkerIn(t *testing.T) {
	out := "C:\\Users\\me\\runnerkit\\runners\\a\\bin\\Runner.Worker.exe\r\n"
	if !WindowsWorkerIn(out, `C:\Users\me\runnerkit\runners\a`) {
		t.Error("worker of this runner")
	}
	if !WindowsWorkerIn(out, `c:\users\ME\runnerkit\runners\a\`) {
		t.Error("case and trailing separator must not matter")
	}
	if WindowsWorkerIn(out, `C:\Users\me\runnerkit\runners\b`) {
		t.Error("worker of another runner must not count")
	}
	if WindowsWorkerIn("\r\n", "") || !WindowsWorkerIn(out, "") {
		t.Error("an empty dir means anywhere")
	}
}

func TestWorkerInDir(t *testing.T) {
	ps := "/usr/bin/sshd -D\n/home/me/rk/runners/a/bin/Runner.Worker spawnclient 12 14\n"
	if !WorkerInDir(ps, "/home/me/rk/runners/a") || !WorkerInDir(ps, "/home/me/rk/runners/a/") {
		t.Error("worker of this runner")
	}
	if WorkerInDir(ps, "/home/me/rk/runners/b") || WorkerInDir(ps, "/home/me/rk/runners/a/x") {
		t.Error("worker of another runner must not count")
	}
}

func TestChildrenBusy(t *testing.T) {
	ps := ` 10     1 /usr/bin/sshd
 200   100 /home/me/rk/runners/a/forgejo-runner daemon
 300   200 /bin/bash -e /tmp/step.sh
 400     1 /home/me/rk/runners/b/forgejo-runner daemon
`
	if !ChildrenBusy(ps, "/home/me/rk/runners/a", 0) {
		t.Error("runner a has a child running")
	}
	if ChildrenBusy(ps, "/home/me/rk/runners/b", 0) {
		t.Error("runner b is idle")
	}
	if !ChildrenBusy(ps, "", 200) || ChildrenBusy(ps, "", 300) {
		t.Error("by pid")
	}
}

func TestWindowsChildrenBusy(t *testing.T) {
	out := "20|4|C:\\rk\\runners\\a\\forgejo-runner.exe\r\n30|20|C:\\Windows\\System32\\cmd.exe\r\n40|4|C:\\rk\\runners\\b\\forgejo-runner.exe\r\n"
	if !WindowsChildrenBusy(out, `C:\rk\runners\a`, 0) || WindowsChildrenBusy(out, `C:\rk\runners\b`, 0) {
		t.Error("per runner folder")
	}
}
