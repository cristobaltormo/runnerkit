package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/dist"
	"github.com/cristobaltormo/runnerkit/internal/forge"
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

func (a *App) provider(kind forge.Kind, baseURL, token string) (forge.Provider, error) {
	cfg := forge.Config{Kind: kind, BaseURL: baseURL, Token: token, HTTP: a.HTTP, Mirror: a.Mirrors}
	if kind == forge.GitHub {
		cfg.API = a.API
		cfg.BaseURL = a.GitHub
	}
	return forge.New(cfg)
}

func (a *App) resolveTarget(raw, forced string) (forge.Kind, string, string, error) {
	target, isURL, err := forge.ParseTarget(raw)
	if err != nil {
		return "", "", "", err
	}
	kind := target.Kind
	if forced != "" {
		kind, err = forge.ParseKind(forced)
		if err != nil {
			return "", "", "", err
		}
	}
	if !isURL {
		if kind != "" && kind != forge.GitHub {
			return "", "", "", fmt.Errorf("give the address of the server, for example https://git.example.com/%s", target.Scope)
		}
		return forge.GitHub, a.GitHub, target.Scope, nil
	}
	if kind == "" {
		kind, err = forge.Detect(a.HTTP, target.BaseURL)
		if err != nil {
			return "", "", "", err
		}
	}
	if kind == forge.GitHub {
		return kind, a.GitHub, target.Scope, nil
	}
	return kind, target.BaseURL, target.Scope, nil
}

func (a *App) installRunner(p forge.Provider, kind forge.Kind, dir, binary string) (string, error) {
	if binary != "" {
		return "local file", copyBinary(binary, filepath.Join(dir, forge.BinaryName(kind, a.GOOS)))
	}
	dl, err := p.Download(a.GOOS, a.GOARCH)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(a.Out, "Downloading the %s runner %s for %s/%s\n", kind, dl.Version, a.GOOS, a.GOARCH)
	file, err := dist.Fetch(a.HTTP, dl.Name, dl.URL, dl.SHA256, filepath.Join(a.DataDir, "cache"))
	if err != nil {
		return "", err
	}
	if dl.Archive {
		return dl.Version, dist.Extract(file, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dl.Version, copyBinary(file, filepath.Join(dir, forge.BinaryName(kind, a.GOOS)))
}

func copyBinary(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (a *App) cmdAdd(args []string) error {
	fs := a.flags("add")
	name := fs.String("name", "", "runner name (default: <hostname>-<scope>)")
	labels := fs.String("labels", "", "extra labels, comma separated")
	token := fs.String("token", "", "access token (default: RUNNERKIT_TOKEN, then the variable of the forge, then the gh CLI for GitHub)")
	forced := fs.String("forge", "", "github, forgejo, gitea or gitlab (default: detected from the address)")
	binary := fs.String("runner-binary", "", "use this runner program instead of downloading one (not for GitHub)")
	allowPublic := fs.Bool("allow-public", false, "allow a public repository (see docs/security.md)")
	noBattery := fs.Bool("no-pause-on-battery", false, "keep running on battery")
	noStart := fs.Bool("no-start", false, "install but do not start")
	group := fs.String("group", "", "runner group (GitHub organizations only)")
	replace := fs.Bool("replace", false, "take over a runner of the same name already registered on GitHub")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: runnerkit add <owner/repo | organization | https://server/owner/repo> [flags]")
	}
	kind, baseURL, scope, err := a.resolveTarget(pos[0], *forced)
	if err != nil {
		return err
	}
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
	defer signal.Reset(os.Interrupt, syscall.SIGTERM)
	tok, err := a.Token(kind, *token)
	if err != nil {
		if kind == forge.Gogs {
			_, perr := forge.New(forge.Config{Kind: kind})
			return perr
		}
		return err
	}
	p, err := a.provider(kind, baseURL, tok)
	if err != nil {
		return err
	}
	if err := p.CheckScope(scope); err != nil {
		return err
	}
	if *group != "" && (kind != forge.GitHub || strings.Contains(scope, "/")) {
		return fmt.Errorf("--group only applies to GitHub organizations")
	}
	if *binary != "" && kind == forge.GitHub {
		return fmt.Errorf("--runner-binary is not available for GitHub, the official runner is always downloaded")
	}
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

	private, err := p.IsPrivate(scope)
	if err != nil {
		return err
	}
	if !private && !*allowPublic {
		return fmt.Errorf("%s is a public repository. A pull request from a fork could run its own code on this machine. Use hosted runners for public repositories, or pass --allow-public if you understand the risk", scope)
	}
	if kind == forge.GitHub && !strings.Contains(scope, "/") {
		fmt.Fprintf(a.Out, "%s is an organization: make sure the runner group only serves private repositories.\n", scope)
	}
	if !*replace {
		taken, err := p.NameTaken(scope, *name)
		if err != nil {
			return err
		}
		if taken {
			return fmt.Errorf("a runner called %s is already registered for %s, probably on another machine; pick another name with --name or pass --replace to take it over", *name, scope)
		}
	}

	dir := filepath.Join(a.DataDir, "runners", *name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists, remove it or pick another name", dir)
	}
	cleanup := func(cause error) error {
		os.RemoveAll(dir)
		return cause
	}
	version, err := a.installRunner(p, kind, dir, *binary)
	if err != nil {
		if errors.Is(err, forge.ErrNoRunnerBinary) {
			return cleanup(fmt.Errorf("%w\nYou can build or download a runner yourself and pass it with --runner-binary", err))
		}
		return cleanup(err)
	}
	if kind == forge.GitLab {
		os.MkdirAll(filepath.Join(dir, "builds"), 0o755)
	}

	extra := []string{}
	for _, l := range strings.Split(*labels, ",") {
		if l = strings.TrimSpace(l); l != "" {
			extra = append(extra, l)
		}
	}
	allLabels := forge.RunnerLabels(kind, a.GOOS, a.GOARCH, extra)
	fmt.Fprintln(a.Out, "Registering the runner")
	reg, err := p.Register(forge.Registration{Dir: dir, Scope: scope, Name: *name, Labels: allLabels, Group: *group,
		Replace: *replace, GOOS: a.GOOS, Exec: forge.Exec(a.Exec)})
	if err != nil {
		return cleanup(fmt.Errorf("registering the runner: %w", err))
	}
	registered := func(cause error) error {
		_ = p.Deregister(forge.Record{Dir: dir, Scope: scope, Name: *name, RunnerID: reg.RunnerID, GOOS: a.GOOS, Exec: forge.Exec(a.Exec)})
		return cleanup(cause)
	}

	r := store.Runner{
		Name:           *name,
		Forge:          string(kind),
		URL:            baseURL,
		RunnerID:       reg.RunnerID,
		Scope:          scope,
		Labels:         allLabels,
		Dir:            dir,
		PauseOnBattery: !*noBattery,
		Version:        version,
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

	fmt.Fprintf(a.Out, "\nRunner %s is installed", *name)
	if !*noStart {
		fmt.Fprint(a.Out, " and running")
	}
	fmt.Fprintf(a.Out, ".\nTo use it, put this in your workflow:\n\n  %s\n\n", forge.Snippet(kind, a.GOOS, a.GOARCH))
	if kind != forge.GitHub {
		fmt.Fprintln(a.Out, "Jobs run directly on this machine, without a container.")
	}
	if kind == forge.GitLab && a.GOOS != "windows" {
		a.warnAboutBashLogout()
	}
	return nil
}

func (a *App) warnAboutBashLogout() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, ".bash_logout"))
	if err == nil && strings.Contains(string(data), "clear_console") {
		fmt.Fprintf(a.Out, "Warning: %s clears the console, which makes GitLab shell jobs fail with\n\"prepare environment: exit status 1\". Delete that file or remove its last lines.\n", filepath.Join(home, ".bash_logout"))
	}
}
