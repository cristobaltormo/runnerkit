package cli

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/forge"
)

func (a *App) cmdDoctor(args []string) error {
	fs := a.flags("doctor")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	problems := 0
	check := func(label string, err error, hint string) {
		if err != nil {
			problems++
			fmt.Fprintf(a.Out, "FAIL  %s: %v\n      %s\n", label, err, hint)
			return
		}
		fmt.Fprintf(a.Out, "ok    %s\n", label)
	}

	_, err := a.platform()
	check("platform supported ("+a.GOOS+"/"+a.GOARCH+")", err, "GitHub publishes runners for Linux, macOS and Windows on x64 and arm64 only")

	if a.IsRoot() {
		check("not running as root", fmt.Errorf("running as root"), "use a normal user account")
	} else {
		check("not running as root", nil, "")
	}

	_, err = exec.LookPath("git")
	check("git is installed", err, "workflows that use actions/checkout need git on the PATH")

	_, err = a.Token(forge.GitHub, "")
	if err != nil {
		fmt.Fprintf(a.Out, "note  no GitHub token found (%v); only needed for GitHub\n", err)
	} else {
		fmt.Fprintln(a.Out, "ok    GitHub token available")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(a.API + "/zen")
	if err == nil {
		resp.Body.Close()
	}
	check("GitHub API reachable", err, "check the network, proxy or firewall (only needed for GitHub)")

	check("data folder writable", writable(a.DataDir), "set RUNNERKIT_HOME to a folder you can write to")

	if free, err := diskFree(a.DataDir); err == nil {
		if free < 10<<30 {
			fmt.Fprintf(a.Out, "WARN  only %.1f GiB free where runners live; builds often need more\n", float64(free)/(1<<30))
		} else {
			fmt.Fprintf(a.Out, "ok    %.0f GiB free\n", float64(free)/(1<<30))
		}
	}

	switch a.GOOS {
	case "linux":
		_, err = exec.LookPath("systemctl")
		check("systemd user services available", err, "runnerkit needs systemd; on other init systems run `runnerkit supervise <name>` yourself")
		user := os.Getenv("USER")
		if out, err := exec.Command("loginctl", "show-user", user, "-p", "Linger").Output(); err == nil && string(out) != "Linger=yes\n" {
			fmt.Fprintf(a.Out, "WARN  lingering is off: runners stop when you log out. Fix: loginctl enable-linger %s\n", user)
		}
	case "darwin":
		_, err = exec.LookPath("launchctl")
		check("launchd available", err, "launchctl was not found")
	case "windows":
		_, err = exec.LookPath("schtasks")
		check("Task Scheduler available", err, "schtasks.exe was not found")
	}

	if problems > 0 {
		return fmt.Errorf("%d problem(s) found", problems)
	}
	fmt.Fprintln(a.Out, "\nThis machine is ready: runnerkit add <owner/repo>")
	return nil
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}
