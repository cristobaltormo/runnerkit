//go:build !windows

package supervise

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeProbe struct {
	mu      sync.Mutex
	battery bool
	busy    bool
	killed  int
	cmd     *exec.Cmd
}

func (f *fakeProbe) OnBattery() bool       { f.mu.Lock(); defer f.mu.Unlock(); return f.battery }
func (f *fakeProbe) Busy(string, int) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.busy }
func (f *fakeProbe) Terminate(pid int) error {
	f.mu.Lock()
	f.killed++
	f.mu.Unlock()
	return f.cmd.Process.Kill()
}
func (f *fakeProbe) set(battery, busy bool) {
	f.mu.Lock()
	f.battery, f.busy = battery, busy
	f.mu.Unlock()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func setup(t *testing.T, probe *fakeProbe) (Config, *syncBuffer) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho started\nexec sleep 60\n"), 0o755)
	cfg := Config{Name: "t", Dir: dir, Probe: probe, Poll: 20 * time.Millisecond, Drain: time.Second,
		Command: func(d string) *exec.Cmd {
			c := exec.Command(filepath.Join(d, "run.sh"))
			probe.mu.Lock()
			probe.cmd = c
			probe.mu.Unlock()
			return c
		}}
	return cfg, &syncBuffer{}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunsAndStopsOnCancel(t *testing.T) {
	probe := &fakeProbe{}
	cfg, log := setup(t, probe)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, log) }()
	waitFor(t, "runner start", func() bool { return strings.Contains(log.String(), "runner started") })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if probe.killed != 1 {
		t.Fatalf("expected one termination, got %d", probe.killed)
	}
}

func TestBatteryPausesOnlyWhenIdle(t *testing.T) {
	probe := &fakeProbe{}
	cfg, log := setup(t, probe)
	cfg.PauseOnBattery = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, log) }()
	waitFor(t, "runner start", func() bool { return strings.Contains(log.String(), "runner started") })

	probe.set(true, true)
	time.Sleep(150 * time.Millisecond)
	if probe.killed != 0 {
		t.Fatal("a busy runner must not be stopped on battery")
	}
	probe.set(true, false)
	waitFor(t, "stop on battery", func() bool { return strings.Contains(log.String(), "on battery and idle") })
	probe.set(false, false)
	waitFor(t, "restart after power", func() bool { return strings.Count(log.String(), "runner started") >= 2 })
	cancel()
	<-done
}

func TestLogFileRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.log")
	l, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 7; i++ {
		if _, err := l.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	cur, _ := os.Stat(path)
	old, err := os.Stat(path + ".1")
	if err != nil || cur.Size() > maxLogSize || old.Size() > maxLogSize {
		t.Fatalf("log not capped: %v %v %v", cur, old, err)
	}
}

func TestSecretsAreNotPassedToTheRunner(t *testing.T) {
	env := withoutSecrets([]string{"PATH=/bin", "GH_TOKEN=a", "github_token=b", "RUNNERKIT_TOKEN=c", "HOME=/h"})
	if len(env) != 2 || env[0] != "PATH=/bin" || env[1] != "HOME=/h" {
		t.Fatalf("unexpected env: %v", env)
	}
}

func TestBackoff(t *testing.T) {
	if backoff(1) != 5*time.Second || backoff(2) != 10*time.Second || backoff(20) != 5*time.Minute {
		t.Fatal("unexpected backoff")
	}
}
