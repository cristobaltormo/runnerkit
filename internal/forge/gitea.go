package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrNoRunnerBinary = errors.New("no official runner download for this system")

type gitea struct {
	kind   Kind
	api    *apiClient
	base   string
	httpc  *http.Client
	mirror Mirrors
}

func newGitea(cfg Config) *gitea {
	mirror := cfg.Mirror
	if mirror.ForgejoReleases == "" {
		mirror.ForgejoReleases = "https://code.forgejo.org/api/v1/repos/forgejo/runner/releases/latest"
	}
	if mirror.GiteaReleases == "" {
		mirror.GiteaReleases = "https://gitea.com/api/v1/repos/gitea/runner/releases/latest"
	}
	return &gitea{
		kind:   cfg.Kind,
		base:   strings.TrimRight(cfg.BaseURL, "/"),
		httpc:  cfg.HTTP,
		mirror: mirror,
		api: &apiClient{base: strings.TrimRight(cfg.BaseURL, "/") + "/api/v1", http: cfg.HTTP,
			authHeader: "Authorization", authValue: "token " + cfg.Token},
	}
}

func (g *gitea) Kind() Kind { return g.kind }

func (g *gitea) CheckScope(scope string) error {
	owner, repo, ok := strings.Cut(scope, "/")
	if !ok || !safeName.MatchString(owner) || !safeName.MatchString(repo) {
		return fmt.Errorf("%q is not a repository: use owner/repo (organization runners are only supported on GitHub for now)", scope)
	}
	return nil
}

var nameChars = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type nameRule struct{}

func (nameRule) MatchString(s string) bool {
	return s != "." && s != ".." && nameChars.MatchString(s)
}

var safeName = nameRule{}

func repoPath(scope string) string {
	owner, repo, _ := strings.Cut(scope, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

func (g *gitea) IsPrivate(scope string) (bool, error) {
	var out struct {
		Private bool `json:"private"`
	}
	if err := g.api.do(http.MethodGet, repoPath(scope), nil, &out); err != nil {
		return false, g.explain(err)
	}
	return out.Private, nil
}

func (g *gitea) explain(err error) error {
	var he *httpError
	if errors.As(err, &he) {
		switch he.Status {
		case 401:
			return fmt.Errorf("%w (the token is missing or wrong)", err)
		case 403, 404:
			return fmt.Errorf("%w (the token needs repository write access, and the repository must exist; Actions must be enabled on the instance)", err)
		}
	}
	return err
}

type runnerEntry struct {
	ID   json.Number `json:"id"`
	Name string      `json:"name"`
}

func (g *gitea) runners(scope string) ([]runnerEntry, bool, error) {
	var all []runnerEntry
	for page := 1; page <= 20; page++ {
		var raw json.RawMessage
		err := g.api.do(http.MethodGet, fmt.Sprintf("%s/actions/runners?limit=50&page=%d", repoPath(scope), page), nil, &raw)
		var he *httpError
		if errors.As(err, &he) && (he.Status == 404 || he.Status == 405) && page == 1 {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, g.explain(err)
		}
		var list []runnerEntry
		if json.Unmarshal(raw, &list) != nil {
			var wrapped struct {
				Runners []runnerEntry `json:"runners"`
			}
			if err := json.Unmarshal(raw, &wrapped); err != nil {
				return nil, false, err
			}
			list = wrapped.Runners
		}
		all = append(all, list...)
		if len(list) < 50 {
			break
		}
	}
	return all, true, nil
}

func (g *gitea) NameTaken(scope, name string) (bool, error) {
	list, ok, err := g.runners(scope)
	if err != nil || !ok {
		return false, err
	}
	for _, r := range list {
		if r.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (g *gitea) registrationToken(scope string) (string, error) {
	path := repoPath(scope) + "/actions/runners/registration-token"
	methods := []string{http.MethodGet, http.MethodPost}
	if g.kind == Gitea {
		methods = []string{http.MethodPost, http.MethodGet}
	}
	var last error
	for _, method := range methods {
		var out struct {
			Token string `json:"token"`
		}
		err := g.api.do(method, path, nil, &out)
		if err == nil && out.Token != "" {
			return out.Token, nil
		}
		last = err
		var he *httpError
		if err != nil && !(errors.As(err, &he) && (he.Status == 404 || he.Status == 405)) {
			break
		}
	}
	if last == nil {
		last = errors.New("the server returned an empty registration token")
	}
	return "", g.explain(last)
}

func (g *gitea) Download(goos, goarch string) (Download, error) {
	arch := map[string]string{"amd64": "amd64", "arm64": "arm64"}[goarch]
	if arch == "" || (goos != "linux" && goos != "darwin" && goos != "windows") {
		return Download{}, fmt.Errorf("%w: %s/%s", ErrNoRunnerBinary, goos, goarch)
	}
	if g.kind == Forgejo && goos != "linux" {
		return Download{}, fmt.Errorf("%w: Forgejo publishes its runner for Linux only; build it yourself and pass --runner-binary", ErrNoRunnerBinary)
	}
	apiURL := g.mirror.GiteaReleases
	prefix := "gitea-runner"
	if g.kind == Forgejo {
		apiURL = g.mirror.ForgejoReleases
		prefix = "forgejo-runner"
	}
	data, err := get(g.httpc, apiURL)
	if err != nil {
		return Download{}, err
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return Download{}, err
	}
	version := strings.TrimPrefix(rel.Tag, "v")
	want := fmt.Sprintf("%s-%s-%s-%s", prefix, version, goos, arch)
	if goos == "windows" {
		want += ".exe"
	}
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.URL
	}
	binURL, ok := urls[want]
	if !ok {
		return Download{}, fmt.Errorf("%w: release %s has no %s", ErrNoRunnerBinary, version, want)
	}
	var sum string
	if sumURL, ok := urls[want+".sha256"]; ok {
		text, err := get(g.httpc, sumURL)
		if err != nil {
			return Download{}, err
		}
		sum = sumFromLine(string(text), want)
	} else if sumURL, ok := urls["checksums.txt"]; ok {
		text, err := get(g.httpc, sumURL)
		if err != nil {
			return Download{}, err
		}
		sum = sumFromLine(string(text), want)
	}
	if sum == "" {
		return Download{}, errors.New("the release publishes no checksum for this download, refusing to install it unverified")
	}
	return Download{Version: version, Name: want, URL: binURL, SHA256: sum}, nil
}

func (g *gitea) Register(req Registration) (Registered, error) {
	token, err := g.registrationToken(req.Scope)
	if err != nil {
		return Registered{}, err
	}
	bin := filepath.Join(req.Dir, BinaryName(g.kind, req.GOOS))
	args := []string{"register", "--no-interactive", "--instance", g.base, "--name", req.Name,
		"--labels", HostLabels(req.Labels)}
	var env []string
	if g.kind == Gitea {
		env = []string{"GITEA_RUNNER_REGISTRATION_TOKEN=" + token}
	} else {
		args = append(args, "--token", token)
	}
	if err := req.Exec(req.Dir, env, bin, args...); err != nil {
		return Registered{}, redactToken(err, token)
	}
	id := readRunnerID(filepath.Join(req.Dir, ".runner"))
	if id == "" {
		return Registered{}, errors.New("the runner registered but its id could not be read from the .runner file")
	}
	return Registered{RunnerID: id}, nil
}

func readRunnerID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var r struct {
		ID json.Number `json:"id"`
	}
	if json.Unmarshal(data, &r) != nil {
		return ""
	}
	return r.ID.String()
}

func (g *gitea) Deregister(rec Record) error {
	id := rec.RunnerID
	if id == "" {
		list, ok, err := g.runners(rec.Scope)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("the runner id is unknown")
		}
		for _, r := range list {
			if r.Name == rec.Name {
				id = r.ID.String()
			}
		}
		if id == "" {
			return nil
		}
	}
	path := repoPath(rec.Scope) + "/actions/runners/" + url.PathEscape(id)
	err := g.api.do(http.MethodDelete, path, nil, nil)
	var he *httpError
	if errors.As(err, &he) && he.Status == 404 {
		return nil
	}
	if err != nil {
		return g.explain(err)
	}
	return nil
}
