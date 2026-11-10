package ghapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const DefaultAPI = "https://api.github.com"

type Client struct {
	API   string
	Token string
	HTTP  *http.Client
}

type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

type Release struct {
	Version string
	Assets  []Asset
	Body    string
}

var scopePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9._-]+)?$`)

func ValidScope(scope string) error {
	if !scopePattern.MatchString(scope) {
		return fmt.Errorf("%q is not a repository (owner/name) or an organization", scope)
	}
	return nil
}

func IsRepo(scope string) bool {
	return strings.Contains(scope, "/")
}

func runnersPath(scope string) string {
	if IsRepo(scope) {
		return "/repos/" + scope + "/actions/runners"
	}
	return "/orgs/" + scope + "/actions/runners"
}

func (c *Client) request(method, path string, out any) error {
	api := c.API
	if api == "" {
		api = DefaultAPI
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	req, err := http.NewRequest(method, strings.TrimRight(api, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return &Error{Status: resp.StatusCode, Path: path, Message: apiMessage(body)}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

func apiMessage(body []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &m) == nil && m.Message != "" {
		return m.Message
	}
	return strings.TrimSpace(string(body))
}

type Error struct {
	Status  int
	Path    string
	Message string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("GitHub answered %d for %s: %s", e.Status, e.Path, e.Message)
	switch e.Status {
	case 401:
		return msg + " (the token is missing, expired or wrong)"
	case 403, 404:
		return msg + " (the token needs administration write access to the repository, or the self-hosted runners permission for an organization; a 404 also means the repository does not exist or the token cannot see it)"
	}
	return msg
}

func (c *Client) RegistrationToken(scope string) (string, error) {
	return c.token(scope, "registration-token")
}

func (c *Client) RemovalToken(scope string) (string, error) {
	return c.token(scope, "remove-token")
}

func (c *Client) token(scope, kind string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := c.request(http.MethodPost, runnersPath(scope)+"/"+kind, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("GitHub returned an empty token")
	}
	return out.Token, nil
}

func (c *Client) IsPrivate(repo string) (bool, error) {
	var out struct {
		Private bool `json:"private"`
	}
	if err := c.request(http.MethodGet, "/repos/"+repo, &out); err != nil {
		return false, err
	}
	return out.Private, nil
}

func (c *Client) LatestRunner() (Release, error) {
	var out struct {
		Tag    string  `json:"tag_name"`
		Body   string  `json:"body"`
		Assets []Asset `json:"assets"`
	}
	if err := c.request(http.MethodGet, "/repos/actions/runner/releases/latest", &out); err != nil {
		return Release{}, err
	}
	return Release{Version: strings.TrimPrefix(out.Tag, "v"), Assets: out.Assets, Body: out.Body}, nil
}

func ResolveToken(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	for _, key := range []string{"RUNNERKIT_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(key); v != "" {
			return v, nil
		}
	}
	if path, err := exec.LookPath("gh"); err == nil {
		out, err := exec.Command(path, "auth", "token").Output()
		if err == nil {
			if t := strings.TrimSpace(string(out)); t != "" {
				return t, nil
			}
		}
	}
	return "", errors.New("no GitHub token found: run `gh auth login`, or set GH_TOKEN, or pass --token")
}
