package dist

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cristobaltormo/runnerkit/internal/ghapi"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestPlatform(t *testing.T) {
	cases := map[[2]string]string{
		{"linux", "amd64"}: "linux-x64", {"linux", "arm"}: "linux-arm",
		{"darwin", "arm64"}: "osx-arm64", {"windows", "amd64"}: "win-x64",
	}
	for in, want := range cases {
		if got, err := Platform(in[0], in[1]); err != nil || got != want {
			t.Errorf("%v: %q %v", in, got, err)
		}
	}
	if _, err := Platform("darwin", "arm"); err == nil {
		t.Error("darwin/arm must be unsupported")
	}
	if _, err := Platform("freebsd", "amd64"); err == nil {
		t.Error("freebsd must be unsupported")
	}
}

func TestExpectedSum(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	withDigest := ghapi.Asset{Digest: "sha256:" + hash}
	if got, err := ExpectedSum(ghapi.Release{}, withDigest, "linux-x64"); err != nil || got != hash {
		t.Fatalf("digest: %q %v", got, err)
	}
	rel := ghapi.Release{Body: "<!-- BEGIN SHA linux-x64 -->" + hash + "<!-- END SHA linux-x64 -->"}
	if got, err := ExpectedSum(rel, ghapi.Asset{}, "linux-x64"); err != nil || got != hash {
		t.Fatalf("body: %q %v", got, err)
	}
	if _, err := ExpectedSum(rel, ghapi.Asset{}, "osx-x64"); err == nil {
		t.Fatal("a missing checksum must be an error")
	}
}

func TestFetchVerifiesAndCaches(t *testing.T) {
	data := tarGz(t, map[string]string{"run.sh": "x"})
	sum := sha256.Sum256(data)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(data)
	}))
	defer srv.Close()
	asset := ghapi.Asset{Name: "actions-runner-linux-x64-1.tar.gz", URL: srv.URL}
	cache := t.TempDir()
	if _, err := Fetch(nil, asset, hex.EncodeToString(sum[:]), cache); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(nil, asset, hex.EncodeToString(sum[:]), cache); err != nil || hits != 1 {
		t.Fatalf("second fetch must come from the cache: hits=%d err=%v", hits, err)
	}
	if _, err := Fetch(nil, asset, strings.Repeat("0", 64), t.TempDir()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected a mismatch, got %v", err)
	}
}

func TestExtractTar(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "r.tar.gz")
	os.WriteFile(archive, tarGz(t, map[string]string{"bin/Runner.Listener": "l", "run.sh": "#!/bin/sh"}), 0o600)
	dest := t.TempDir()
	if err := Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("run.sh must be executable: %v", info.Mode())
	}
}

func TestExtractRejectsEscape(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	os.WriteFile(archive, tarGz(t, map[string]string{"../evil": "x"}), 0o600)
	if err := Extract(archive, t.TempDir()); err == nil {
		t.Fatal("a path outside the destination must be refused")
	}
	zipPath := filepath.Join(t.TempDir(), "evil.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../evil")
	w.Write([]byte("x"))
	zw.Close()
	os.WriteFile(zipPath, buf.Bytes(), 0o600)
	if err := Extract(zipPath, t.TempDir()); err == nil {
		t.Fatal("a zip entry outside the destination must be refused")
	}
}

func TestExtractRejectsAbsoluteLink(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc"})
	tw.Close()
	gz.Close()
	archive := filepath.Join(t.TempDir(), "l.tar.gz")
	os.WriteFile(archive, buf.Bytes(), 0o600)
	if err := Extract(archive, t.TempDir()); err == nil {
		t.Fatal("an absolute link must be refused")
	}
}

func TestWorkflowLabels(t *testing.T) {
	if os, arch := WorkflowLabels("darwin", "arm64"); os != "macos" || arch != "arm64" {
		t.Fatalf("got %s %s", os, arch)
	}
}
