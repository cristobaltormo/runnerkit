package sys

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type Probe interface {
	OnBattery() bool
	Busy(dir string, rootPID int) bool
	Terminate(pid int) error
}

type Host struct{}

func (Host) OnBattery() bool {
	switch runtime.GOOS {
	case "linux":
		return linuxOnBattery("/sys/class/power_supply")
	case "darwin":
		out, err := exec.Command("pmset", "-g", "batt").Output()
		return err == nil && PmsetOnBattery(string(out))
	case "windows":
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"(Get-CimInstance Win32_Battery | Select-Object -First 1).BatteryStatus").Output()
		return err == nil && WindowsOnBattery(string(out))
	}
	return false
}

func PmsetOnBattery(out string) bool {
	return strings.Contains(out, "'Battery Power'")
}

func WindowsOnBattery(out string) bool {
	return strings.TrimSpace(out) == "1"
}

func linuxOnBattery(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	onBattery := false
	for _, e := range entries {
		base := filepath.Join(dir, e.Name())
		kind, _ := os.ReadFile(filepath.Join(base, "type"))
		switch strings.TrimSpace(string(kind)) {
		case "Mains", "USB":
			if online, _ := os.ReadFile(filepath.Join(base, "online")); strings.TrimSpace(string(online)) == "1" {
				return false
			}
		case "Battery":
			if status, _ := os.ReadFile(filepath.Join(base, "status")); strings.TrimSpace(string(status)) == "Discharging" {
				onBattery = true
			}
		}
	}
	return onBattery
}

func (Host) Busy(dir string, rootPID int) bool {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
			`Get-CimInstance Win32_Process -Filter "Name='Runner.Worker.exe'" | ForEach-Object { $_.ExecutablePath }`).Output()
		return err == nil && WindowsWorkerIn(string(out), dir)
	}
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return false
	}
	return WorkerBelow(string(out), rootPID)
}

func WindowsWorkerIn(out, dir string) bool {
	prefix := strings.ToLower(strings.TrimRight(dir, `\/`))
	for _, line := range strings.Split(out, "\n") {
		path := strings.ToLower(strings.TrimSpace(line))
		if path == "" {
			continue
		}
		if prefix == "" || strings.HasPrefix(path, prefix+`\`) {
			return true
		}
	}
	return false
}

func WorkerBelow(psOutput string, rootPID int) bool {
	children := map[int][]int{}
	names := map[int]string{}
	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
		names[pid] = filepath.Base(strings.Join(fields[2:], " "))
	}
	if rootPID <= 0 {
		for _, name := range names {
			if strings.HasPrefix(name, "Runner.Worker") {
				return true
			}
		}
		return false
	}
	queue := []int{rootPID}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if strings.HasPrefix(names[c], "Runner.Worker") {
				return true
			}
			queue = append(queue, c)
		}
	}
	return false
}

func (Host) Terminate(pid int) error {
	if runtime.GOOS == "windows" {
		return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
