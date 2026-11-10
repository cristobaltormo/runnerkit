package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/dist"
	"github.com/cristobaltormo/runnerkit/internal/forge"
	"github.com/cristobaltormo/runnerkit/internal/paths"
	"github.com/cristobaltormo/runnerkit/internal/service"
	"github.com/cristobaltormo/runnerkit/internal/store"
	"github.com/cristobaltormo/runnerkit/internal/sys"
)

type App struct {
	Out, Err io.Writer
	Version  string
	DataDir  string
	API      string
	GitHub   string
	HTTP     *http.Client
	Service  service.Manager
	Exe      string
	Probe    sys.Probe
	GOOS     string
	GOARCH   string
	IsRoot   func() bool
	Token    func(kind forge.Kind, flag string) (string, error)
	Mirrors  forge.Mirrors
	Exec     func(dir string, env []string, name string, args ...string) error
}

func Main(args []string, version string) int {
	app, err := newApp(version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "runnerkit:", err)
		return 1
	}
	return app.Run(args)
}

func newApp(version string) (*App, error) {
	data, err := paths.DataDir()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	api := os.Getenv("RUNNERKIT_API_URL")
	if api == "" {
		api = "https://api.github.com"
	}
	gh := os.Getenv("RUNNERKIT_GITHUB_URL")
	if gh == "" {
		gh = "https://github.com"
	}
	return &App{
		Out:     os.Stdout,
		Err:     os.Stderr,
		Version: version,
		DataDir: data,
		API:     api,
		GitHub:  gh,
		HTTP:    &http.Client{Timeout: 10 * time.Minute},
		Service: service.Manager{GOOS: runtime.GOOS, Home: home, UID: os.Getuid(), Run: runCombined},
		Exe:     exe,
		Probe:   sys.Host{},
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		IsRoot:  func() bool { return runtime.GOOS != "windows" && os.Geteuid() == 0 },
		Token:   forge.ResolveToken,
		Exec:    runCommand,
	}, nil
}

func runCombined(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func runCommand(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

const usage = `runnerkit sets up self-hosted CI runners for GitHub, Forgejo, Gitea and GitLab on this machine.

Usage:
  runnerkit add <owner/repo | https://server/owner/repo>   install a runner and start it
  runnerkit list                                      list the runners on this machine
  runnerkit status [name]                             show the state of one or all runners
  runnerkit start|stop|restart <name>                 control a runner
  runnerkit logs <name> [-n lines]                    show the end of a runner log
  runnerkit clean [name] [flags]                      delete old work folders and logs
  runnerkit remove <name> [flags]                     unregister and delete a runner
  runnerkit doctor                                    check that this machine is ready
  runnerkit version

Run "runnerkit <command> -h" for the flags of a command.
`

func (a *App) Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Err, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "add":
		err = a.cmdAdd(rest)
	case "list", "ls":
		err = a.cmdList(rest)
	case "status":
		err = a.cmdStatus(rest)
	case "start", "stop", "restart":
		err = a.cmdControl(cmd, rest)
	case "logs":
		err = a.cmdLogs(rest)
	case "clean":
		err = a.cmdClean(rest)
	case "remove", "rm":
		err = a.cmdRemove(rest)
	case "doctor":
		err = a.cmdDoctor(rest)
	case "supervise":
		err = a.cmdSupervise(rest)
	case "version", "--version", "-v":
		fmt.Fprintln(a.Out, "runnerkit", a.Version)
	case "help", "--help", "-h":
		fmt.Fprint(a.Out, usage)
	default:
		fmt.Fprintf(a.Err, "runnerkit: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(a.Err, "runnerkit:", err)
		return 1
	}
	return 0
}

func (a *App) store() *store.Store { return store.Open(a.DataDir) }

func (a *App) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("runnerkit "+name, flag.ContinueOnError)
	fs.SetOutput(a.Err)
	return fs
}

func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (a *App) platform() (string, error) {
	return dist.Platform(a.GOOS, a.GOARCH)
}

func (a *App) User() string {
	if a.GOOS != "windows" {
		return ""
	}
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

func (a *App) logPath(name string) string {
	return filepath.Join(a.DataDir, "logs", name+".log")
}
