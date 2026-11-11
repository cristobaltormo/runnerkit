//go:build !windows

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cristobaltormo/runnerkit/internal/forge"
)

const fakeRunner = `#!/bin/sh
case "$1" in
register) echo '{"id":7,"uuid":"u"}' > .runner ;;
daemon) exec sleep 60 ;;
esac
`

type forgejoServer struct {
	srv     *httptest.Server
	deleted atomic.Int32
	private bool
}

func newForgejoServer(t *testing.T) *forgejoServer {
	f := &forgejoServer{private: true}
	sum := sha256.Sum256([]byte(fakeRunner))
	hexsum := hex.EncodeToString(sum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/forgejo/v1/version", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"version":"16"}`) })
	mux.HandleFunc("GET /api/v1/repos/me/app", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, `{"private":%v}`, f.private) })
	mux.HandleFunc("GET /api/v1/repos/me/app/actions/runners", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[{"name":"taken"}]`) })
	mux.HandleFunc("GET /api/v1/repos/me/app/actions/runners/registration-token", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"token":"REG"}`) })
	mux.HandleFunc("DELETE /api/v1/repos/me/app/actions/runners/7", func(w http.ResponseWriter, r *http.Request) {
		f.deleted.Add(1)
		w.WriteHeader(204)
	})
	mux.HandleFunc("GET /releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1.0.0","assets":[{"name":"forgejo-runner-1.0.0-linux-amd64","browser_download_url":"%[1]s/bin"},{"name":"forgejo-runner-1.0.0-linux-amd64.sha256","browser_download_url":"%[1]s/sum"}]}`, f.srv.URL)
	})
	mux.HandleFunc("GET /bin", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, fakeRunner) })
	mux.HandleFunc("GET /sum", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  forgejo-runner-1.0.0-linux-amd64\n", hexsum)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestAddAForgejoRunnerFromItsAddress(t *testing.T) {
	f := newForgejoServer(t)
	e := newEnv(t)
	e.app.Mirrors = forge.Mirrors{ForgejoReleases: f.srv.URL + "/releases"}
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "tk", nil }

	if code := e.app.Run([]string{"add", f.srv.URL + "/me/app", "--name", "fj"}); code != 0 {
		t.Fatalf("add failed: %s %s", e.out, e.errBuf)
	}
	if !strings.Contains(e.out.String(), "runs-on: [runnerkit, linux, x64]") || !strings.Contains(e.out.String(), "without a container") {
		t.Errorf("unexpected output:\n%s", e.out)
	}
	r, err := e.app.store().Get("fj")
	if err != nil || r.Forge != "forgejo" || r.RunnerID != "7" || r.URL != f.srv.URL {
		t.Fatalf("stored runner: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "forgejo-runner")); err != nil {
		t.Errorf("the runner program must be in its folder: %v", err)
	}
	e.out.Reset()
	e.app.Run([]string{"list"})
	if !strings.Contains(e.out.String(), "forgejo") {
		t.Errorf("list must show the forge:\n%s", e.out)
	}
	if code := e.app.Run([]string{"remove", "fj"}); code != 0 || f.deleted.Load() != 1 {
		t.Fatalf("remove must delete the runner on the server: %d %s", f.deleted.Load(), e.errBuf)
	}
}

func TestAddChecksTheServerBeforeInstalling(t *testing.T) {
	f := newForgejoServer(t)
	e := newEnv(t)
	e.app.Mirrors = forge.Mirrors{ForgejoReleases: f.srv.URL + "/releases"}
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "tk", nil }

	if e.app.Run([]string{"add", f.srv.URL + "/me/app", "--name", "taken"}) == 0 || !strings.Contains(e.errBuf.String(), "already registered") {
		t.Errorf("a name taken on the server must be refused: %s", e.errBuf)
	}
	f.private = false
	e.errBuf.Reset()
	if e.app.Run([]string{"add", f.srv.URL + "/me/app", "--name", "pub"}) == 0 || !strings.Contains(e.errBuf.String(), "public") {
		t.Errorf("a public repository must be refused: %s", e.errBuf)
	}
	e.errBuf.Reset()
	if e.app.Run([]string{"add", "me/app", "--forge", "forgejo"}) == 0 || !strings.Contains(e.errBuf.String(), "address of the server") {
		t.Errorf("a non-GitHub forge needs an address: %s", e.errBuf)
	}
	if rs, _ := e.app.store().List(); len(rs) != 0 {
		t.Errorf("nothing may be recorded after a refusal: %v", rs)
	}
}

func TestForgejoWithoutAMacRunnerExplainsTheWayOut(t *testing.T) {
	f := newForgejoServer(t)
	e := newEnv(t)
	e.app.GOOS = "darwin"
	e.app.GOARCH = "arm64"
	e.app.Mirrors = forge.Mirrors{ForgejoReleases: f.srv.URL + "/releases"}
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "tk", nil }
	if e.app.Run([]string{"add", f.srv.URL + "/me/app", "--name", "mac"}) == 0 || !strings.Contains(e.errBuf.String(), "--runner-binary") {
		t.Fatalf("the error must point to --runner-binary: %s", e.errBuf)
	}
	if _, err := os.Stat(filepath.Join(e.app.DataDir, "runners", "mac")); err == nil {
		t.Error("a failed install must leave no folder")
	}
	own := filepath.Join(t.TempDir(), "forgejo-runner")
	os.WriteFile(own, []byte(fakeRunner), 0o755)
	e.app.Exec = runCommand
	e.app.Service.GOOS = "linux"
	if code := e.app.Run([]string{"add", f.srv.URL + "/me/app", "--name", "mac", "--runner-binary", own, "--no-start"}); code != 0 {
		t.Fatalf("--runner-binary must work: %s", e.errBuf)
	}
}

func TestGogsIsExplainedNotInstalled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/user" {
			w.WriteHeader(401)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	e := newEnv(t)
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "", fmt.Errorf("no token") }
	if e.app.Run([]string{"add", srv.URL + "/me/app"}) == 0 || !strings.Contains(e.errBuf.String(), "Gogs has no built-in CI") {
		t.Fatalf("Gogs must be explained even without a token: %s", e.errBuf)
	}
}

func TestRemoveWithoutATokenStopsNothing(t *testing.T) {
	f := newForgejoServer(t)
	e := newEnv(t)
	e.app.Mirrors = forge.Mirrors{ForgejoReleases: f.srv.URL + "/releases"}
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "tk", nil }
	if code := e.app.Run([]string{"add", f.srv.URL + "/me/app", "--name", "fj", "--no-start"}); code != 0 {
		t.Fatalf("add: %s", e.errBuf)
	}
	e.calls = nil
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "", fmt.Errorf("no token") }
	if e.app.Run([]string{"remove", "fj"}) == 0 || !strings.Contains(e.errBuf.String(), "Nothing was stopped") {
		t.Fatalf("remove without a token must fail early: %s", e.errBuf)
	}
	for _, c := range e.calls {
		if strings.Contains(c, "stop") || strings.Contains(c, "kill") {
			t.Errorf("the service must not be touched: %v", e.calls)
		}
	}
	if _, err := e.app.store().Get("fj"); err != nil {
		t.Error("the runner must still be recorded")
	}
}

func TestPlainHTTPAndReplaceAreRefusedOutsideGitHub(t *testing.T) {
	e := newEnv(t)
	e.app.Token = func(kind forge.Kind, flag string) (string, error) { return "tk", nil }
	if e.app.Run([]string{"add", "http://git.example.com/me/app", "--forge", "gitea"}) == 0 || !strings.Contains(e.errBuf.String(), "--allow-http") {
		t.Errorf("public plain http must be refused: %s", e.errBuf)
	}
	e.errBuf.Reset()
	if e.app.Run([]string{"add", "https://git.example.com/me/app", "--forge", "gitea", "--replace"}) == 0 || !strings.Contains(e.errBuf.String(), "only available for GitHub") {
		t.Errorf("--replace outside GitHub: %s", e.errBuf)
	}
}
