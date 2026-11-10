package forge

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in      string
		kind    Kind
		base    string
		scope   string
		isURL   bool
		wantErr bool
	}{
		{"owner/repo", "", "", "owner/repo", false, false},
		{"owner/repo.git", "", "", "owner/repo", false, false},
		{"https://github.com/owner/repo", GitHub, "https://github.com", "owner/repo", true, false},
		{"https://git.example.com:3000/team/app.git", "", "https://git.example.com:3000", "team/app", true, false},
		{"https://gitlab.example.com/group/sub/app/", "", "https://gitlab.example.com", "group/sub/app", true, false},
		{"https://", "", "", "", true, true},
	}
	for _, c := range cases {
		got, isURL, err := ParseTarget(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err %v", c.in, err)
			continue
		}
		if c.wantErr {
			continue
		}
		if got.Kind != c.kind || got.BaseURL != c.base || got.Scope != c.scope || isURL != c.isURL {
			t.Errorf("%s: got %+v url=%v", c.in, got, isURL)
		}
	}
}

func TestParseKindAndGogs(t *testing.T) {
	if k, err := ParseKind("GitLab"); err != nil || k != GitLab {
		t.Fatal("case must not matter")
	}
	if _, err := ParseKind("bitbucket"); err == nil {
		t.Fatal("unknown forge")
	}
	_, err := New(Config{Kind: Gogs})
	if err == nil || !strings.Contains(err.Error(), "no built-in CI") {
		t.Fatalf("Gogs must be refused with an explanation: %v", err)
	}
}

func TestDetect(t *testing.T) {
	serve := func(routes map[string]struct {
		code int
		body string
	}) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rt, ok := routes[r.URL.Path]; ok {
				w.WriteHeader(rt.code)
				fmt.Fprint(w, rt.body)
				return
			}
			http.NotFound(w, r)
		}))
	}
	type route = struct {
		code int
		body string
	}
	cases := map[Kind]map[string]route{
		Forgejo: {"/api/forgejo/v1/version": {200, `{"version":"16"}`}, "/api/v1/version": {200, `{"version":"16"}`}},
		Gitea:   {"/api/v1/version": {200, `{"version":"1.27.0"}`}},
		GitLab:  {"/api/v4/version": {401, `{"message":"401 Unauthorized"}`}},
		Gogs:    {"/api/v1/user": {401, `{"message":"Unauthorized"}`}},
	}
	loginRedirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/version" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/users/sign_in" {
			fmt.Fprint(w, "<html>version</html>")
			return
		}
		http.Redirect(w, r, "/users/sign_in", http.StatusFound)
	}))
	defer loginRedirect.Close()
	if got, err := Detect(loginRedirect.Client(), loginRedirect.URL); err != nil || got != GitLab {
		t.Errorf("a login redirect must not look like Gitea: %q %v", got, err)
	}
	for want, routes := range cases {
		srv := serve(routes)
		got, err := Detect(srv.Client(), srv.URL)
		srv.Close()
		if err != nil || got != want {
			t.Errorf("%s: got %q %v", want, got, err)
		}
	}
	srv := serve(nil)
	defer srv.Close()
	if _, err := Detect(srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "--forge") {
		t.Errorf("an unknown server must suggest --forge: %v", err)
	}
}

func TestResolveToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "gl")
	t.Setenv("GITEA_TOKEN", "")
	t.Setenv("FORGEJO_TOKEN", "fj")
	t.Setenv("RUNNERKIT_TOKEN", "")
	if tok, _ := ResolveToken(GitLab, ""); tok != "gl" {
		t.Error("gitlab variable")
	}
	if tok, _ := ResolveToken(Gitea, ""); tok != "fj" {
		t.Error("gitea falls back to the forgejo variable")
	}
	if tok, _ := ResolveToken(GitLab, "flag"); tok != "flag" {
		t.Error("flag wins")
	}
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("PATH", "")
	if _, err := ResolveToken(GitLab, ""); err == nil {
		t.Error("no token must be an error")
	}
}

func TestLabelsAndSnippets(t *testing.T) {
	if got := Snippet(Forgejo, "linux", "amd64"); got != "runs-on: [runnerkit, linux, x64]" {
		t.Error(got)
	}
	if got := Snippet(GitLab, "darwin", "arm64"); got != "tags: [runnerkit, macos, arm64]" {
		t.Error(got)
	}
	if got := Snippet(GitHub, "windows", "amd64"); got != "runs-on: [self-hosted, windows, x64, runnerkit]" {
		t.Error(got)
	}
	if got := HostLabels(RunnerLabels(Gitea, "linux", "amd64", []string{"gpu"})); got != "runnerkit:host,linux:host,x64:host,gpu:host" {
		t.Error(got)
	}
	if got := RunnerLabels(GitHub, "linux", "amd64", nil); len(got) != 1 || got[0] != "runnerkit" {
		t.Error(got)
	}
}

type recorded struct {
	dir  string
	env  []string
	name string
	args []string
}

func recorder(rec *[]recorded, onRun func(dir string)) Exec {
	return func(dir string, env []string, name string, args ...string) error {
		*rec = append(*rec, recorded{dir, env, name, args})
		if onRun != nil {
			onRun(dir)
		}
		return nil
	}
}

func TestGiteaFamilyRegisterAndDeregister(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/repos/me/app", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token tk" {
			http.Error(w, `{"message":"no"}`, 401)
			return
		}
		fmt.Fprint(w, `{"private":true}`)
	})
	mux.HandleFunc("GET /api/v1/repos/me/app/actions/runners/registration-token", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"REG"}`)
	})
	mux.HandleFunc("GET /api/v1/repos/me/app/actions/runners", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"name":"taken"}]`)
	})
	mux.HandleFunc("DELETE /api/v1/repos/me/app/actions/runners/7", func(w http.ResponseWriter, r *http.Request) {
		deleted = r.URL.Path
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newGitea(Config{Kind: Forgejo, BaseURL: srv.URL, Token: "tk", HTTP: srv.Client()})

	if private, err := p.IsPrivate("me/app"); err != nil || !private {
		t.Fatalf("private: %v %v", private, err)
	}
	if taken, _ := p.NameTaken("me/app", "taken"); !taken {
		t.Error("taken")
	}
	if taken, _ := p.NameTaken("me/app", "free"); taken {
		t.Error("free")
	}
	if err := p.CheckScope("me"); err == nil {
		t.Error("an organization must be refused")
	}

	dir := t.TempDir()
	var calls []recorded
	reg, err := p.Register(Registration{Dir: dir, Scope: "me/app", Name: "box", Labels: []string{"runnerkit", "linux"}, GOOS: "linux",
		Exec: recorder(&calls, func(d string) { os.WriteFile(filepath.Join(d, ".runner"), []byte(`{"id":7,"uuid":"u"}`), 0o600) })})
	if err != nil || reg.RunnerID != "7" {
		t.Fatalf("register: %+v %v", reg, err)
	}
	joined := strings.Join(calls[0].args, " ")
	for _, want := range []string{"register", "--no-interactive", "--instance " + srv.URL, "--name box", "--labels runnerkit:host,linux:host", "--token REG"} {
		if !strings.Contains(joined, want) {
			t.Errorf("register args lack %q: %s", want, joined)
		}
	}
	if filepath.Base(calls[0].name) != "forgejo-runner" {
		t.Errorf("binary %s", calls[0].name)
	}
	if err := p.Deregister(Record{Scope: "me/app", RunnerID: "7"}); err != nil || deleted == "" {
		t.Errorf("deregister: %v %q", err, deleted)
	}
	if err := p.Deregister(Record{Scope: "me/app"}); err == nil {
		t.Error("unknown id must be an error")
	}
	p2 := newGitea(Config{Kind: Forgejo, BaseURL: srv.URL, Token: "wrong", HTTP: srv.Client()})
	if _, err := p2.IsPrivate("me/app"); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("401 hint: %v", err)
	}
}

func TestGiteaFamilyDownloads(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("GET /fj", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v13.2.1","assets":[{"name":"forgejo-runner-13.2.1-linux-amd64","browser_download_url":"%[1]s/bin"},{"name":"forgejo-runner-13.2.1-linux-amd64.sha256","browser_download_url":"%[1]s/sum"}]}`, srv.URL)
	})
	mux.HandleFunc("GET /gt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v5.4.1","assets":[{"name":"gitea-runner-5.4.1-windows-amd64.exe","browser_download_url":"%[1]s/bin"},{"name":"checksums.txt","browser_download_url":"%[1]s/sums"}]}`, srv.URL)
	})
	mux.HandleFunc("GET /sum", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  forgejo-runner-13.2.1-linux-amd64\n", hash)
	})
	mux.HandleFunc("GET /sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  gitea-runner-5.4.1-linux-amd64\n%s  gitea-runner-5.4.1-windows-amd64.exe\n", strings.Repeat("00", 32), hash)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	mirror := Mirrors{ForgejoReleases: srv.URL + "/fj", GiteaReleases: srv.URL + "/gt"}

	fj := newGitea(Config{Kind: Forgejo, BaseURL: "http://x", HTTP: srv.Client(), Mirror: mirror})
	d, err := fj.Download("linux", "amd64")
	if err != nil || d.SHA256 != hash || d.Version != "13.2.1" || d.Archive {
		t.Fatalf("forgejo: %+v %v", d, err)
	}
	if _, err := fj.Download("darwin", "arm64"); !errors.Is(err, ErrNoRunnerBinary) {
		t.Errorf("forgejo has no macOS runner: %v", err)
	}
	gt := newGitea(Config{Kind: Gitea, BaseURL: "http://x", HTTP: srv.Client(), Mirror: mirror})
	d, err = gt.Download("windows", "amd64")
	if err != nil || d.SHA256 != hash || d.Name != "gitea-runner-5.4.1-windows-amd64.exe" {
		t.Fatalf("gitea: %+v %v", d, err)
	}
	if _, err := gt.Download("linux", "amd64"); !errors.Is(err, ErrNoRunnerBinary) {
		t.Errorf("an asset missing from the release must be reported: %v", err)
	}
}

func TestGitLabLifecycle(t *testing.T) {
	var created, deleted bool
	var form string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/projects/{path}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("path") != "grp/sub/app" || r.Header.Get("PRIVATE-TOKEN") != "tk" {
			http.Error(w, `{"message":"404 Not Found"}`, 404)
			return
		}
		fmt.Fprint(w, `{"id":42,"visibility":"private"}`)
	})
	mux.HandleFunc("GET /api/v4/projects/42/runners", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"description":"taken"}]`)
	})
	mux.HandleFunc("POST /api/v4/user/runners", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm.Encode()
		created = true
		fmt.Fprint(w, `{"id":9,"token":"glrt-SECRET"}`)
	})
	mux.HandleFunc("DELETE /api/v4/runners/9", func(w http.ResponseWriter, r *http.Request) {
		deleted = true
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newGitLab(Config{Kind: GitLab, BaseURL: srv.URL, Token: "tk", HTTP: srv.Client()})

	if err := p.CheckScope("group"); err == nil {
		t.Error("a group must be refused")
	}
	if private, err := p.IsPrivate("grp/sub/app"); err != nil || !private {
		t.Fatalf("private: %v %v", private, err)
	}
	if taken, _ := p.NameTaken("grp/sub/app", "taken"); !taken {
		t.Error("taken")
	}
	var calls []recorded
	reg, err := p.Register(Registration{Dir: t.TempDir(), Scope: "grp/sub/app", Name: "box", Labels: []string{"runnerkit", "linux"}, GOOS: "linux", Exec: recorder(&calls, nil)})
	if err != nil || reg.RunnerID != "9" || !created {
		t.Fatalf("register: %+v %v", reg, err)
	}
	for _, want := range []string{"project_id=42", "runner_type=project_type", "tag_list=runnerkit%2Clinux", "run_untagged=false"} {
		if !strings.Contains(form, want) {
			t.Errorf("create form lacks %q: %s", want, form)
		}
	}
	joined := strings.Join(calls[0].args, " ")
	if !strings.Contains(joined, "--token glrt-SECRET") || !strings.Contains(joined, "--executor shell") {
		t.Errorf("register args: %s", joined)
	}
	if err := p.Deregister(Record{RunnerID: "9"}); err != nil || !deleted {
		t.Errorf("deregister: %v", err)
	}

	failing := func(string, []string, string, ...string) error { return errors.New("boom glrt-SECRET") }
	deleted = false
	_, err = p.Register(Registration{Dir: t.TempDir(), Scope: "grp/sub/app", Name: "box", GOOS: "linux", Exec: failing})
	if err == nil || strings.Contains(err.Error(), "glrt-SECRET") || !deleted {
		t.Errorf("a failed register must hide the token and delete the runner: %v deleted=%v", err, deleted)
	}
}

func TestGitLabDownload(t *testing.T) {
	hash := strings.Repeat("cd", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s\tbinaries/gitlab-runner-darwin-arm64\n%s\tbinaries/gitlab-runner-windows-amd64.exe\n", hash, strings.Repeat("11", 32))
	}))
	defer srv.Close()
	p := newGitLab(Config{Kind: GitLab, BaseURL: "http://x", HTTP: srv.Client(), Mirror: Mirrors{GitLabDownloads: srv.URL}})
	d, err := p.Download("darwin", "arm64")
	if err != nil || d.SHA256 != hash || !strings.HasSuffix(d.URL, "/binaries/gitlab-runner-darwin-arm64") || d.Archive {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := p.Download("linux", "amd64"); err == nil {
		t.Error("a missing checksum must be refused")
	}
}

func TestDaemonCommands(t *testing.T) {
	gh := Daemon(GitHub, "/r", "linux")
	if filepath.Base(gh.Path) != "run.sh" {
		t.Error(gh.Path)
	}
	gl := Daemon(GitLab, "/r", "linux")
	if !strings.Contains(strings.Join(gl.Args, " "), "run --config "+filepath.Join("/r", "config.toml")) {
		t.Error(gl.Args)
	}
	fj := Daemon(Forgejo, "/r", "windows")
	if !strings.HasSuffix(fj.Path, "forgejo-runner.exe") || fj.Args[len(fj.Args)-1] != "daemon" {
		t.Error(fj.Args)
	}
}

func TestGiteaUsesPostForTheRegistrationTokenAndListsAnObject(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/repos/me/app/actions/runners/registration-token", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"GT"}`)
	})
	mux.HandleFunc("GET /api/v1/repos/me/app/actions/runners/registration-token", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"runner with id 0: resource does not exist"}`, 404)
	})
	mux.HandleFunc("GET /api/v1/repos/me/app/actions/runners", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"runners":[{"name":"taken"}],"total_count":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := newGitea(Config{Kind: Gitea, BaseURL: srv.URL, Token: "tk", HTTP: srv.Client()})
	if taken, err := p.NameTaken("me/app", "taken"); err != nil || !taken {
		t.Errorf("object listing: %v %v", taken, err)
	}
	var calls []recorded
	if _, err := p.Register(Registration{Dir: t.TempDir(), Scope: "me/app", Name: "b", GOOS: "linux", Exec: recorder(&calls, nil)}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls[0].args, " "), "--token GT") {
		t.Errorf("token: %v", calls[0].args)
	}
	pf := newGitea(Config{Kind: Forgejo, BaseURL: srv.URL, Token: "tk", HTTP: srv.Client()})
	if _, err := pf.registrationToken("me/app"); err != nil {
		t.Errorf("forgejo kind must fall back to POST when GET is refused: %v", err)
	}
}
