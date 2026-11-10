package cli

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/dist"
	"github.com/cristobaltormo/runnerkit/internal/ghapi"
	"github.com/cristobaltormo/runnerkit/internal/service"
	"github.com/cristobaltormo/runnerkit/internal/store"
)

var nameClean = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func defaultName(scope string) string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	host = nameClean.ReplaceAllString(host, "-")
	if len(host) > 20 {
		host = host[:20]
	}
	name := nameClean.ReplaceAllString(host+"-"+strings.ReplaceAll(scope, "/", "-"), "-")
	name = strings.Trim(name, "-._")
	if len(name) > 63 {
		name = strings.Trim(name[len(name)-63:], "-._")
	}
	return name
}

func (a *App) cmdAdd(args []string) error {
	fs := a.flags("add")
	name := fs.String("name", "", "runner name (default: <hostname>-<scope>)")
	labels := fs.String("labels", "", "extra labels, comma separated")
	token := fs.String("token", "", "GitHub token (default: RUNNERKIT_TOKEN, GH_TOKEN, GITHUB_TOKEN, then the gh CLI)")
	allowPublic := fs.Bool("allow-public", false, "allow a public repository (see docs/security.md)")
	noBattery := fs.Bool("no-pause-on-battery", false, "keep running on battery")
	noStart := fs.Bool("no-start", false, "install but do not start")
	group := fs.String("group", "", "runner group (organizations only)")
	replace := fs.Bool("replace", false, "take over a runner of the same name already registered on GitHub")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: runnerkit add <owner/repo | organization> [flags]")
	}
	scope := pos[0]
	if err := ghapi.ValidScope(scope); err != nil {
		return err
	}
	if *group != "" && ghapi.IsRepo(scope) {
		return fmt.Errorf("--group only applies to organizations")
	}
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	defer signal.Reset(os.Interrupt, syscall.SIGTERM)
	if a.IsRoot() {
		return fmt.Errorf("do not run this as root: the runner executes workflow code and should use a normal user account")
	}
	if *name == "" {
		*name = defaultName(scope)
	}
	if err := store.ValidName(*name); err != nil {
		return err
	}
	if _, err := a.store().Get(*name); err == nil {
		return fmt.Errorf("a runner called %s already exists here, pick another name with --name", *name)
	}
	platform, err := a.platform()
	if err != nil {
		return err
	}
	tok, err := a.Token(*token)
	if err != nil {
		return err
	}
	api := a.client(tok)

	if ghapi.IsRepo(scope) {
		private, err := api.IsPrivate(scope)
		if err != nil {
			return err
		}
		if !private && !*allowPublic {
			return fmt.Errorf("%s is a public repository. A pull request from a fork could run its own code on this machine. Use GitHub-hosted runners for public repositories, or pass --allow-public if you understand the risk", scope)
		}
	} else {
		fmt.Fprintf(a.Out, "%s is an organization: make sure the runner group only serves private repositories.\n", scope)
	}

	if !*replace {
		names, err := api.RunnerNames(scope, *name)
		if err != nil {
			return err
		}
		for _, n := range names {
			if n == *name {
				return fmt.Errorf("a runner called %s is already registered on GitHub for %s, probably on another machine; pick another name with --name or pass --replace to take it over", *name, scope)
			}
		}
	}

	rel, err := api.LatestRunner()
	if err != nil {
		return err
	}
	asset, err := dist.AssetFor(rel, platform)
	if err != nil {
		return err
	}
	sum, err := dist.ExpectedSum(rel, asset, platform)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Downloading the GitHub runner %s for %s\n", rel.Version, platform)
	archive, err := dist.Fetch(a.HTTP, asset, sum, filepath.Join(a.DataDir, "cache"))
	if err != nil {
		return err
	}

	dir := filepath.Join(a.DataDir, "runners", *name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists, remove it or pick another name", dir)
	}
	if err := dist.Extract(archive, dir); err != nil {
		os.RemoveAll(dir)
		return err
	}

	cleanup := func(cause error) error {
		os.RemoveAll(dir)
		return cause
	}

	reg, err := api.RegistrationToken(scope)
	if err != nil {
		return cleanup(err)
	}
	allLabels := []string{"runnerkit"}
	for _, l := range strings.Split(*labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			allLabels = append(allLabels, l)
		}
	}
	cfgArgs := []string{"--unattended", "--url", a.GitHub + "/" + scope,
		"--name", *name, "--labels", strings.Join(allLabels, ","), "--work", "_work"}
	if *replace {
		cfgArgs = append(cfgArgs, "--replace")
	}
	if *group != "" {
		cfgArgs = append(cfgArgs, "--runnergroup", *group)
	}
	script := filepath.Join(dir, a.configScript())
	fmt.Fprintln(a.Out, "Registering the runner")
	if err := a.Exec(dir, []string{"ACTIONS_RUNNER_INPUT_TOKEN=" + reg}, script, cfgArgs...); err != nil {
		return cleanup(fmt.Errorf("registering the runner: %w", redact(err, reg)))
	}
	registered := func(cause error) error {
		if rm, err := api.RemovalToken(scope); err == nil {
			_ = a.Exec(dir, []string{"ACTIONS_RUNNER_INPUT_TOKEN=" + rm}, script, "remove")
		}
		return cleanup(cause)
	}

	r := store.Runner{
		Name:           *name,
		Scope:          scope,
		Labels:         allLabels,
		Dir:            dir,
		PauseOnBattery: !*noBattery,
		Version:        rel.Version,
		Created:        time.Now().UTC(),
	}
	if err := a.store().Put(r); err != nil {
		return registered(err)
	}
	if err := a.Service.Install(service.Spec{Name: *name, Exe: a.Exe, Home: a.DataDir, User: a.User()}); err != nil {
		a.store().Delete(*name)
		a.Service.Remove(*name)
		return registered(fmt.Errorf("installing the service: %w", err))
	}
	if !*noStart {
		if err := a.Service.Start(*name); err != nil {
			return fmt.Errorf("the runner is installed but did not start: %w", err)
		}
	}

	osLabel, arch := dist.WorkflowLabels(a.GOOS, a.GOARCH)
	fmt.Fprintf(a.Out, "\nRunner %s is installed", *name)
	if !*noStart {
		fmt.Fprint(a.Out, " and running")
	}
	fmt.Fprintf(a.Out, ".\nTo use it, put this in your workflow:\n\n  runs-on: [self-hosted, %s, %s, runnerkit]\n\n", osLabel, arch)
	if a.GOOS == "linux" {
		if u, err := user.Current(); err == nil {
			fmt.Fprintf(a.Out, "To keep it running when you are logged out, run once: loginctl enable-linger %s\n", u.Username)
		}
	}
	return nil
}

func redact(err error, secret string) error {
	if secret == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "***"))
}
