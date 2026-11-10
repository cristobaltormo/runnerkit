package forge

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type gitlab struct {
	api    *apiClient
	base   string
	httpc  *http.Client
	mirror Mirrors
}

func newGitLab(cfg Config) *gitlab {
	mirror := cfg.Mirror
	if mirror.GitLabDownloads == "" {
		mirror.GitLabDownloads = "https://gitlab-runner-downloads.s3.amazonaws.com/latest"
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	return &gitlab{
		base:   base,
		httpc:  cfg.HTTP,
		mirror: mirror,
		api: &apiClient{base: base + "/api/v4", http: cfg.HTTP,
			authHeader: "PRIVATE-TOKEN", authValue: cfg.Token},
	}
}

func (g *gitlab) Kind() Kind { return GitLab }

func (g *gitlab) CheckScope(scope string) error {
	if strings.Count(scope, "/") < 1 || strings.HasPrefix(scope, "/") || strings.HasSuffix(scope, "/") {
		return fmt.Errorf("%q is not a project: use group/project (group runners are only supported on GitHub for now)", scope)
	}
	return nil
}

type project struct {
	ID         int    `json:"id"`
	Visibility string `json:"visibility"`
}

func (g *gitlab) project(scope string) (project, error) {
	var p project
	if err := g.api.do(http.MethodGet, "/projects/"+url.PathEscape(scope), nil, &p); err != nil {
		return project{}, g.explain(err)
	}
	return p, nil
}

func (g *gitlab) explain(err error) error {
	var he *httpError
	if errors.As(err, &he) {
		switch he.Status {
		case 401:
			return fmt.Errorf("%w (the token is missing or wrong)", err)
		case 403, 404:
			return fmt.Errorf("%w (the token needs the api scope and Maintainer access to the project; a 404 also means the project does not exist)", err)
		}
	}
	return err
}

func (g *gitlab) IsPrivate(scope string) (bool, error) {
	p, err := g.project(scope)
	if err != nil {
		return false, err
	}
	return p.Visibility != "public", nil
}

func (g *gitlab) NameTaken(scope, name string) (bool, error) {
	p, err := g.project(scope)
	if err != nil {
		return false, err
	}
	var runners []struct {
		Description string `json:"description"`
	}
	if err := g.api.do(http.MethodGet, "/projects/"+strconv.Itoa(p.ID)+"/runners?per_page=100", nil, &runners); err != nil {
		return false, g.explain(err)
	}
	for _, r := range runners {
		if r.Description == name {
			return true, nil
		}
	}
	return false, nil
}

func (g *gitlab) Download(goos, goarch string) (Download, error) {
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[goarch]
	if arch == "" || (goos != "linux" && goos != "darwin" && goos != "windows") {
		return Download{}, fmt.Errorf("%w: %s/%s", ErrNoRunnerBinary, goos, goarch)
	}
	file := fmt.Sprintf("gitlab-runner-%s-%s", goos, arch)
	if goos == "windows" {
		file += ".exe"
	}
	text, err := get(g.httpc, g.mirror.GitLabDownloads+"/release.sha256")
	if err != nil {
		return Download{}, err
	}
	sum := sumFromLine(string(text), "binaries/"+file)
	if sum == "" {
		return Download{}, errors.New("the release publishes no checksum for this download, refusing to install it unverified")
	}
	return Download{Version: "latest", Name: file + "-" + sum[:12], URL: g.mirror.GitLabDownloads + "/binaries/" + file, SHA256: sum}, nil
}

func (g *gitlab) Register(req Registration) (Registered, error) {
	p, err := g.project(req.Scope)
	if err != nil {
		return Registered{}, err
	}
	form := url.Values{
		"runner_type":  {"project_type"},
		"project_id":   {strconv.Itoa(p.ID)},
		"description":  {req.Name},
		"tag_list":     {joinComma(req.Labels)},
		"run_untagged": {"false"},
		"locked":       {"true"},
	}
	var created struct {
		ID    int    `json:"id"`
		Token string `json:"token"`
	}
	if err := g.api.do(http.MethodPost, "/user/runners", form, &created); err != nil {
		return Registered{}, g.explain(err)
	}
	if created.Token == "" {
		return Registered{}, errors.New("GitLab returned no runner token")
	}
	bin := filepath.Join(req.Dir, BinaryName(GitLab, req.GOOS))
	args := []string{"register", "--non-interactive", "--config", filepath.Join(req.Dir, "config.toml"),
		"--url", g.base, "--token", created.Token, "--executor", "shell"}
	if err := req.Exec(req.Dir, nil, bin, args...); err != nil {
		_ = g.api.do(http.MethodDelete, "/runners/"+strconv.Itoa(created.ID), nil, nil)
		return Registered{}, redactToken(err, created.Token)
	}
	return Registered{RunnerID: strconv.Itoa(created.ID)}, nil
}

func (g *gitlab) Deregister(rec Record) error {
	if rec.RunnerID != "" {
		if err := g.api.do(http.MethodDelete, "/runners/"+url.PathEscape(rec.RunnerID), nil, nil); err != nil {
			return g.explain(err)
		}
	}
	return nil
}
