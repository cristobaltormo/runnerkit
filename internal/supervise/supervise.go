package supervise

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/clean"
	"github.com/cristobaltormo/runnerkit/internal/forge"
	"github.com/cristobaltormo/runnerkit/internal/sys"
)

const (
	maxLogSize               = 5 << 20
	idleChecksBeforeStopping = 3
)

type Config struct {
	Name           string
	Dir            string
	PauseOnBattery bool
	Probe          sys.Probe
	Style          sys.Style
	Poll           time.Duration
	Drain          time.Duration
	CleanAfter     time.Duration
	Command        func(dir string) *exec.Cmd
}

func Command(kind forge.Kind) func(dir string) *exec.Cmd {
	return func(dir string) *exec.Cmd {
		cmd := forge.Daemon(kind, dir, runtime.GOOS)
		cmd.Env = append(withoutSecrets(os.Environ()), "RUNNER_MANUALLY_TRAP_SIG=1")
		cmd.WaitDelay = 5 * time.Second
		sys.Detach(cmd)
		return cmd
	}
}

func withoutSecrets(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, secret := range forge.SecretVariables() {
			if strings.EqualFold(name, secret) {
				drop = true
			}
		}
		if !drop {
			kept = append(kept, kv)
		}
	}
	return kept
}

type LogFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func OpenLog(path string) (*LogFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &LogFile{path: path}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *LogFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

func (l *LogFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > maxLogSize {
		l.f.Close()
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			os.Remove(l.path)
		}
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *LogFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func stopChild(cmd *exec.Cmd, exited <-chan error, probe sys.Probe, grace time.Duration) {
	_ = probe.Terminate(cmd.Process.Pid)
	select {
	case <-exited:
		return
	case <-time.After(grace):
	}
	_ = probe.Kill(cmd.Process.Pid)
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
	}
}

func Run(ctx context.Context, cfg Config, log io.Writer) error {
	if cfg.Poll == 0 {
		cfg.Poll = 30 * time.Second
	}
	grace := 30 * time.Second
	if runtime.GOOS == "linux" {
		if cfg.Drain == 0 {
			cfg.Drain = 45 * time.Minute
		}
	} else {
		grace = 8 * time.Second
	}
	if cfg.CleanAfter == 0 {
		cfg.CleanAfter = 14 * 24 * time.Hour
	}
	if cfg.Command == nil {
		cfg.Command = Command(forge.GitHub)
	}
	logf := func(format string, a ...any) {
		fmt.Fprintf(log, "%s supervisor: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, a...))
	}
	failures := 0
	for ctx.Err() == nil {
		if cfg.PauseOnBattery && cfg.Probe.OnBattery() {
			logf("on battery, waiting for power")
			for cfg.Probe.OnBattery() {
				if !sleep(ctx, cfg.Poll) {
					return nil
				}
			}
			logf("power is back")
		}
		if res, err := clean.Run(cfg.Dir, cfg.CleanAfter, false, time.Now()); err == nil && len(res.Removed) > 0 {
			logf("cleaned %d old items, freed %s", len(res.Removed), clean.Human(res.Bytes))
		}
		cmd := cfg.Command(cfg.Dir)
		cmd.Stdout = log
		cmd.Stderr = log
		started := time.Now()
		if err := cmd.Start(); err != nil {
			logf("cannot start the runner: %v", err)
			failures++
			if !sleep(ctx, backoff(failures)) {
				return nil
			}
			continue
		}
		logf("runner started (pid %d)", cmd.Process.Pid)
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()

		pausedForBattery := false
		idleTicks := 0
		ticker := time.NewTicker(cfg.Poll)
	watch:
		for {
			select {
			case err := <-exited:
				logf("runner exited: %v", err)
				break watch
			case <-ticker.C:
				if !cfg.PauseOnBattery || !cfg.Probe.OnBattery() || cfg.Probe.Busy(cfg.Dir, cmd.Process.Pid, cfg.Style) {
					idleTicks = 0
					continue
				}
				if idleTicks++; idleTicks >= idleChecksBeforeStopping {
					logf("on battery and idle, stopping the runner")
					stopChild(cmd, exited, cfg.Probe, grace)
					pausedForBattery = true
					break watch
				}
			case <-ctx.Done():
				deadline := time.Now().Add(cfg.Drain)
				for cfg.Probe.Busy(cfg.Dir, cmd.Process.Pid, cfg.Style) && time.Now().Before(deadline) {
					select {
					case <-exited:
						ticker.Stop()
						return nil
					case <-time.After(min(cfg.Poll, 5*time.Second)):
					}
				}
				logf("stopping the runner")
				stopChild(cmd, exited, cfg.Probe, grace)
				ticker.Stop()
				return nil
			}
		}
		ticker.Stop()
		if pausedForBattery || time.Since(started) > time.Minute {
			failures = 0
		} else {
			failures++
		}
		if pausedForBattery {
			continue
		}
		if !sleep(ctx, backoff(failures)) {
			return nil
		}
	}
	return nil
}

func backoff(failures int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < failures && d < 5*time.Minute; i++ {
		d *= 2
	}
	return min(d, 5*time.Minute)
}
