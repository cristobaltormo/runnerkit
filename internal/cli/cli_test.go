//go:build !windows

package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cristobaltormo/runnerkit/internal/service"
)

type fakeProbe struct{ busy bool }

func (fakeProbe) OnBattery() bool         { return false }
func (f fakeProbe) Busy(string, int) bool { return f.busy }
func (fakeProbe) Terminate(int) error     { return nil }

type env struct {
	app     *App
	out     *bytes.Buffer
	errBuf  *bytes.Buffer
	calls   []string
	private bool
	mu      sync.Mutex
}

func newEnv(t *testing.T) *env {
	e := &env{out: &bytes.Buffer{}, errBuf: &bytes.Buffer{}, private: true}
	files := map[string]string{
		"config.sh": "#!/bin/sh\necho \"$@\" > config-args.txt\necho \"$ACTIONS_RUNNER_INPUT_TOKEN\" > config-token.txt\necho cfg > .runner\n",
		"run.sh":    "#!/bin/sh\nexec sleep 60\n",
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	sum := sha256.Sum256(buf.Bytes())

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/actions/runner/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":"actions-runner-linux-x64-9.9.9.tar.gz","browser_download_url":"%s/dl","digest":"sha256:%s"}]}`, srv.URL, hex.EncodeToString(sum[:]))
	})
	mux.HandleFunc("GET /dl", func(w http.ResponseWriter, r *http.Request) { w.Write(buf.Bytes()) })
	mux.HandleFunc("GET /repos/me/priv", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"private":%v}`, e.private)
	})
	mux.HandleFunc("POST /repos/me/priv/actions/runners/registration-token", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"token":"REGTOKEN"}`)) })
	mux.HandleFunc("POST /repos/me/priv/actions/runners/remove-token", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"token":"RMTOKEN"}`)) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	home := t.TempDir()
	e.app = &App{
		Out: e.out, Err: e.errBuf, Version: "test", DataDir: filepath.Join(home, "data"),
		API: srv.URL, GitHub: "https://github.example", HTTP: srv.Client(),
		Service: service.Manager{GOOS: "linux", Home: home, UID: 1000, Run: func(name string, args ...string) ([]byte, error) {
			e.mu.Lock()
			e.calls = append(e.calls, name+" "+strings.Join(args, " "))
			e.mu.Unlock()
			if len(args) > 1 && args[1] == "is-active" {
				return []byte("active\n"), nil
			}
			return nil, nil
		}},
		Exe: "/bin/runnerkit", Probe: fakeProbe{}, GOOS: "linux", GOARCH: "amd64",
		IsRoot: func() bool { return false },
		Token:  func(flag string) (string, error) { return "tok", nil },
		Exec:   runCommand,
	}
	return e
}

func TestAddListRemove(t *testing.T) {
	e := newEnv(t)
	if code := e.app.Run([]string{"add", "me/priv", "--labels", "gpu, fast", "--name", "box"}); code != 0 {
		t.Fatalf("add failed: %s %s", e.out, e.errBuf)
	}
	if !strings.Contains(e.out.String(), "runs-on: [self-hosted, linux, x64, runnerkit]") {
		t.Errorf("missing workflow snippet:\n%s", e.out)
	}
	args, err := os.ReadFile(filepath.Join(e.app.DataDir, "runners", "box", "config-args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), "REGTOKEN") {
		t.Error("the token must not be on the command line")
	}
	tokenFile, _ := os.ReadFile(filepath.Join(e.app.DataDir, "runners", "box", "config-token.txt"))
	if strings.TrimSpace(string(tokenFile)) != "REGTOKEN" {
		t.Errorf("the token must reach config.sh through the environment, got %q", tokenFile)
	}
	for _, want := range []string{"--unattended", "--url https://github.example/me/priv", "--name box", "--labels runnerkit,gpu,fast", "--replace"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("config.sh args lack %q: %s", want, args)
		}
	}
	calls := strings.Join(e.calls, "\n")
	unit, _ := os.ReadFile(filepath.Join(e.app.Service.Home, ".config", "systemd", "user", "runnerkit-box.service"))
	if !strings.Contains(string(unit), `"--home" "`+e.app.DataDir+`"`) {
		t.Errorf("the service must carry the data folder:\n%s", unit)
	}
	if !strings.Contains(calls, "systemctl --user start runnerkit-box.service") {
		t.Errorf("service not started:\n%s", calls)
	}

	e.out.Reset()
	if e.app.Run([]string{"list"}) != 0 || !strings.Contains(e.out.String(), "box") || !strings.Contains(e.out.String(), "running") {
		t.Errorf("list output:\n%s", e.out)
	}
	if e.app.Run([]string{"add", "me/priv", "--name", "box"}) == 0 {
		t.Error("a duplicate name must be refused")
	}

	e.out.Reset()
	if code := e.app.Run([]string{"remove", "box"}); code != 0 {
		t.Fatalf("remove failed: %s %s", e.out, e.errBuf)
	}
	if _, err := os.Stat(filepath.Join(e.app.DataDir, "runners", "box")); err == nil {
		t.Error("runner folder must be deleted")
	}
	e.out.Reset()
	e.app.Run([]string{"list"})
	if !strings.Contains(e.out.String(), "No runners") {
		t.Errorf("list after remove:\n%s", e.out)
	}
}

func TestAddRefusesPublicRepo(t *testing.T) {
	e := newEnv(t)
	e.private = false
	if code := e.app.Run([]string{"add", "me/priv"}); code == 0 {
		t.Fatal("a public repository must be refused")
	}
	if !strings.Contains(e.errBuf.String(), "public repository") {
		t.Errorf("unhelpful message: %s", e.errBuf)
	}
	if code := e.app.Run([]string{"add", "me/priv", "--allow-public", "--no-start", "--name", "pub"}); code != 0 {
		t.Fatalf("--allow-public must work: %s", e.errBuf)
	}
}

func TestAddRefusesRootAndCleansUpOnFailure(t *testing.T) {
	e := newEnv(t)
	e.app.IsRoot = func() bool { return true }
	if e.app.Run([]string{"add", "me/priv"}) == 0 {
		t.Fatal("root must be refused")
	}
	e.app.IsRoot = func() bool { return false }
	e.app.Exec = func(dir string, env []string, name string, args ...string) error {
		return fmt.Errorf("boom with %s", env[0])
	}
	if e.app.Run([]string{"add", "me/priv", "--name", "bad"}) == 0 {
		t.Fatal("a failing registration must fail")
	}
	if strings.Contains(e.errBuf.String(), "REGTOKEN") {
		t.Errorf("the registration token leaked: %s", e.errBuf)
	}
	if _, err := os.Stat(filepath.Join(e.app.DataDir, "runners", "bad")); err == nil {
		t.Error("a failed add must leave no folder behind")
	}
	if rs, _ := e.app.store().List(); len(rs) != 0 {
		t.Errorf("a failed add must not be recorded: %v", rs)
	}
}

func TestRemoveForceWithoutGitHub(t *testing.T) {
	e := newEnv(t)
	e.app.Run([]string{"add", "me/priv", "--name", "box", "--no-start"})
	e.app.Token = func(string) (string, error) { return "", fmt.Errorf("no token") }
	if e.app.Run([]string{"remove", "box"}) == 0 {
		t.Fatal("without a token, remove must fail unless forced")
	}
	if e.app.Run([]string{"remove", "box", "--force"}) != 0 {
		t.Fatalf("forced remove failed: %s", e.errBuf)
	}
	if !strings.Contains(e.errBuf.String(), "Delete box") {
		t.Errorf("must tell the user to delete it by hand: %s", e.errBuf)
	}
}

func TestUnknownCommandAndVersion(t *testing.T) {
	e := newEnv(t)
	if e.app.Run([]string{"nope"}) != 2 {
		t.Error("unknown command must exit 2")
	}
	if e.app.Run([]string{"version"}) != 0 || !strings.Contains(e.out.String(), "runnerkit test") {
		t.Error("version output")
	}
}

func TestDefaultName(t *testing.T) {
	name := defaultName("My-Org/some.repo")
	if strings.ContainsAny(name, "/ ") || !strings.HasSuffix(name, "My-Org-some.repo") {
		t.Errorf("unexpected name %q", name)
	}
}

func TestStopRefusesWhileAJobRuns(t *testing.T) {
	e := newEnv(t)
	e.app.Run([]string{"add", "me/priv", "--name", "box", "--no-start"})
	e.app.Probe = fakeProbe{busy: true}
	if e.app.Run([]string{"stop", "box"}) == 0 || !strings.Contains(e.errBuf.String(), "job is running") {
		t.Fatalf("stop must be refused while busy: %s", e.errBuf)
	}
	if e.app.Run([]string{"stop", "box", "--force"}) != 0 {
		t.Fatalf("--force must stop anyway: %s", e.errBuf)
	}
}

func TestLogsRejectsBadLineCount(t *testing.T) {
	e := newEnv(t)
	e.app.Run([]string{"add", "me/priv", "--name", "box", "--no-start"})
	if e.app.Run([]string{"logs", "box", "-n", "0"}) == 0 {
		t.Fatal("-n 0 must be refused")
	}
}

func TestFailedInstallDeregistersTheRunner(t *testing.T) {
	e := newEnv(t)
	var calls []string
	e.app.Exec = func(dir string, env []string, name string, args ...string) error {
		calls = append(calls, filepath.Base(name)+" "+strings.Join(args, " "))
		return nil
	}
	e.app.Service.Run = func(name string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "enable" {
			return []byte("nope"), fmt.Errorf("exit 1")
		}
		return nil, nil
	}
	if e.app.Run([]string{"add", "me/priv", "--name", "box"}) == 0 {
		t.Fatal("a failing service install must fail")
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[1], "config.sh remove") {
		t.Fatalf("the runner must be deregistered, calls: %v", calls)
	}
}
