package forge

import (
	"fmt"
	"path/filepath"

	"github.com/cristobaltormo/runnerkit/internal/dist"
	"github.com/cristobaltormo/runnerkit/internal/ghapi"
)

type github struct {
	api    *ghapi.Client
	github string
}

func newGitHub(cfg Config) *github {
	base := cfg.API
	if base == "" {
		base = ghapi.DefaultAPI
	}
	site := cfg.BaseURL
	if site == "" {
		site = "https://github.com"
	}
	return &github{api: &ghapi.Client{API: base, Token: cfg.Token, HTTP: cfg.HTTP}, github: site}
}

func (g *github) Kind() Kind { return GitHub }

func (g *github) CheckScope(scope string) error { return ghapi.ValidScope(scope) }

func (g *github) IsPrivate(scope string) (bool, error) {
	if !ghapi.IsRepo(scope) {
		return true, nil
	}
	return g.api.IsPrivate(scope)
}

func (g *github) NameTaken(scope, name string) (bool, error) {
	names, err := g.api.RunnerNames(scope, name)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == name {
			return true, nil
		}
	}
	return false, nil
}

func (g *github) Download(goos, goarch string) (Download, error) {
	platform, err := dist.Platform(goos, goarch)
	if err != nil {
		return Download{}, err
	}
	rel, err := g.api.LatestRunner()
	if err != nil {
		return Download{}, err
	}
	asset, err := dist.AssetFor(rel, platform)
	if err != nil {
		return Download{}, err
	}
	sum, err := dist.ExpectedSum(rel, asset, platform)
	if err != nil {
		return Download{}, err
	}
	return Download{Version: rel.Version, Name: asset.Name, URL: asset.URL, SHA256: sum, Archive: true}, nil
}

func (g *github) script(dir, goos string) string {
	if goos == "windows" {
		return filepath.Join(dir, "config.cmd")
	}
	return filepath.Join(dir, "config.sh")
}

func (g *github) Register(req Registration) (Registered, error) {
	token, err := g.api.RegistrationToken(req.Scope)
	if err != nil {
		return Registered{}, err
	}
	args := []string{"--unattended", "--url", g.github + "/" + req.Scope, "--name", req.Name,
		"--labels", joinComma(req.Labels), "--work", "_work"}
	if req.Group != "" {
		args = append(args, "--runnergroup", req.Group)
	}
	if req.Replace {
		args = append(args, "--replace")
	}
	if err := req.Exec(req.Dir, []string{"ACTIONS_RUNNER_INPUT_TOKEN=" + token}, g.script(req.Dir, req.GOOS), args...); err != nil {
		return Registered{}, redactToken(err, token)
	}
	return Registered{}, nil
}

func (g *github) Deregister(rec Record) error {
	token, err := g.api.RemovalToken(rec.Scope)
	if err != nil {
		return err
	}
	if err := rec.Exec(rec.Dir, []string{"ACTIONS_RUNNER_INPUT_TOKEN=" + token}, g.script(rec.Dir, rec.GOOS), "remove"); err != nil {
		return redactToken(err, token)
	}
	return nil
}

func (g *github) String() string { return fmt.Sprintf("github(%s)", g.github) }
