package forge

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Kind string

const (
	GitHub  Kind = "github"
	Forgejo Kind = "forgejo"
	Gitea   Kind = "gitea"
	GitLab  Kind = "gitlab"
	Gogs    Kind = "gogs"
)

func ParseKind(s string) (Kind, error) {
	switch k := Kind(strings.ToLower(s)); k {
	case GitHub, Forgejo, Gitea, GitLab, Gogs:
		return k, nil
	}
	return "", fmt.Errorf("unknown forge %q: use github, forgejo, gitea or gitlab", s)
}

type Exec func(dir string, env []string, name string, args ...string) error

type Download struct {
	Version string
	Name    string
	URL     string
	SHA256  string
	Archive bool
}

type Registration struct {
	Dir     string
	Scope   string
	Name    string
	Labels  []string
	Group   string
	Replace bool
	GOOS    string
	Binary  string
	Exec    Exec
}

type Record struct {
	Dir      string
	Scope    string
	Name     string
	RunnerID string
	GOOS     string
	Exec     Exec
}

type Registered struct {
	RunnerID string
}

type Provider interface {
	Kind() Kind
	CheckScope(scope string) error
	IsPrivate(scope string) (bool, error)
	NameTaken(scope, name string) (bool, error)
	Download(goos, goarch string) (Download, error)
	Register(req Registration) (Registered, error)
	Deregister(rec Record) error
}

type Config struct {
	Kind    Kind
	BaseURL string
	Token   string
	HTTP    *http.Client
	API     string
	Mirror  Mirrors
}

type Mirrors struct {
	ForgejoReleases string
	GiteaReleases   string
	GiteaDownloads  string
	GitLabDownloads string
}

func New(cfg Config) (Provider, error) {
	switch cfg.Kind {
	case GitHub:
		return newGitHub(cfg), nil
	case Forgejo, Gitea:
		return newGitea(cfg), nil
	case GitLab:
		return newGitLab(cfg), nil
	case Gogs:
		return nil, errors.New("Gogs has no built-in CI and no runners, so there is nothing for runnerkit to install; see docs/providers.md")
	}
	return nil, fmt.Errorf("unsupported forge %q", cfg.Kind)
}

func BinaryName(kind Kind, goos string) string {
	name := map[Kind]string{Forgejo: "forgejo-runner", Gitea: "gitea-runner", GitLab: "gitlab-runner"}[kind]
	if name != "" && goos == "windows" {
		name += ".exe"
	}
	return name
}

func Daemon(kind Kind, dir, goos string) *exec.Cmd {
	var cmd *exec.Cmd
	switch kind {
	case Forgejo, Gitea:
		cmd = exec.Command(filepath.Join(dir, BinaryName(kind, goos)), "daemon")
	case GitLab:
		cmd = exec.Command(filepath.Join(dir, BinaryName(kind, goos)), "run",
			"--config", filepath.Join(dir, "config.toml"), "--working-directory", filepath.Join(dir, "builds"))
	default:
		script := "run.sh"
		if goos == "windows" {
			script = "run.cmd"
		}
		cmd = exec.Command(filepath.Join(dir, script))
	}
	cmd.Dir = dir
	return cmd
}

func WorkflowLabels(goos, goarch string) (string, string) {
	system := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[goos]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "arm": "arm"}[goarch]
	return system, arch
}

func Snippet(kind Kind, goos, goarch string) string {
	system, arch := WorkflowLabels(goos, goarch)
	switch kind {
	case GitLab:
		return fmt.Sprintf("tags: [runnerkit, %s, %s]", system, arch)
	case Forgejo, Gitea:
		return fmt.Sprintf("runs-on: [runnerkit, %s, %s]", system, arch)
	}
	return fmt.Sprintf("runs-on: [self-hosted, %s, %s, runnerkit]", system, arch)
}

func RunnerLabels(kind Kind, goos, goarch string, extra []string) []string {
	system, arch := WorkflowLabels(goos, goarch)
	labels := []string{"runnerkit"}
	if kind != GitHub {
		labels = append(labels, system, arch)
	}
	return append(labels, extra...)
}

func HostLabels(labels []string) string {
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = l + ":host"
	}
	return strings.Join(out, ",")
}

func SecretVariables() []string {
	return []string{"GH_TOKEN", "GITHUB_TOKEN", "GITLAB_TOKEN", "FORGEJO_TOKEN", "GITEA_TOKEN", "RUNNERKIT_TOKEN"}
}

func ResolveToken(kind Kind, flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	env := []string{"RUNNERKIT_TOKEN"}
	switch kind {
	case GitHub:
		env = append(env, "GH_TOKEN", "GITHUB_TOKEN")
	case GitLab:
		env = append(env, "GITLAB_TOKEN")
	case Forgejo:
		env = append(env, "FORGEJO_TOKEN", "GITEA_TOKEN")
	case Gitea:
		env = append(env, "GITEA_TOKEN", "FORGEJO_TOKEN")
	}
	for _, key := range env {
		if v := os.Getenv(key); v != "" {
			return v, nil
		}
	}
	if kind == GitHub {
		if path, err := exec.LookPath("gh"); err == nil {
			if out, err := exec.Command(path, "auth", "token").Output(); err == nil {
				if t := strings.TrimSpace(string(out)); t != "" {
					return t, nil
				}
			}
		}
		return "", errors.New("no GitHub token found: run `gh auth login`, or set GH_TOKEN, or pass --token")
	}
	return "", fmt.Errorf("no token found: set %s or pass --token", env[len(env)-1])
}

type Target struct {
	Kind    Kind
	BaseURL string
	Scope   string
}

func ParseTarget(raw string) (Target, bool, error) {
	if !strings.Contains(raw, "://") {
		return Target{Scope: strings.TrimSuffix(raw, ".git")}, false, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Target{}, true, fmt.Errorf("%q is not a valid address", raw)
	}
	scope := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	t := Target{BaseURL: u.Scheme + "://" + u.Host, Scope: scope}
	if strings.EqualFold(u.Hostname(), "github.com") {
		t.Kind = GitHub
	}
	return t, true, nil
}

func Detect(httpc *http.Client, baseURL string) (Kind, error) {
	plain := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if httpc != nil {
		plain.Transport = httpc.Transport
		plain.Timeout = httpc.Timeout
	}
	probe := func(path string) (int, string) {
		resp, err := plain.Get(strings.TrimRight(baseURL, "/") + path)
		if err != nil {
			return 0, ""
		}
		defer resp.Body.Close()
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		return resp.StatusCode, string(buf[:n])
	}
	if code, _ := probe("/api/forgejo/v1/version"); code == http.StatusOK {
		return Forgejo, nil
	}
	if code, body := probe("/api/v1/version"); code == http.StatusOK && strings.HasPrefix(strings.TrimSpace(body), "{") && strings.Contains(body, "version") {
		return Gitea, nil
	}
	if code, _ := probe("/api/v4/version"); code == http.StatusOK || code == http.StatusUnauthorized {
		return GitLab, nil
	}
	if code, _ := probe("/api/v1/user"); code == http.StatusUnauthorized || code == http.StatusForbidden {
		return Gogs, nil
	}
	return "", fmt.Errorf("could not tell which forge %s runs; pass --forge github, forgejo, gitea or gitlab", baseURL)
}
