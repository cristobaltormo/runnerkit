package dist

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cristobaltormo/runnerkit/internal/ghapi"
)

func Platform(goos, goarch string) (string, error) {
	systems := map[string]string{"linux": "linux", "darwin": "osx", "windows": "win"}
	arches := map[string]string{"amd64": "x64", "arm64": "arm64", "arm": "arm"}
	system, ok := systems[goos]
	arch, ok2 := arches[goarch]
	if !ok || !ok2 || (goos != "linux" && goarch == "arm") {
		return "", fmt.Errorf("GitHub does not publish a runner for %s/%s", goos, goarch)
	}
	return system + "-" + arch, nil
}

func WorkflowLabels(goos, goarch string) (string, string) {
	system := map[string]string{"linux": "linux", "darwin": "macos", "windows": "windows"}[goos]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "arm": "arm"}[goarch]
	return system, arch
}

func AssetFor(rel ghapi.Release, platform string) (ghapi.Asset, error) {
	prefix := "actions-runner-" + platform + "-"
	for _, a := range rel.Assets {
		if strings.HasPrefix(a.Name, prefix) && (strings.HasSuffix(a.Name, ".tar.gz") || strings.HasSuffix(a.Name, ".zip")) {
			return a, nil
		}
	}
	return ghapi.Asset{}, fmt.Errorf("runner %s has no download for %s", rel.Version, platform)
}

var bodySum = regexp.MustCompile(`<!--\s*BEGIN SHA ([\w.-]+)\s*-->\s*([0-9a-fA-F]{64})\s*<!--\s*END SHA`)

func ExpectedSum(rel ghapi.Release, asset ghapi.Asset, platform string) (string, error) {
	if sum, ok := strings.CutPrefix(asset.Digest, "sha256:"); ok && len(sum) == 64 {
		return strings.ToLower(sum), nil
	}
	for _, m := range bodySum.FindAllStringSubmatch(rel.Body, -1) {
		if m[1] == platform {
			return strings.ToLower(m[2]), nil
		}
	}
	return "", errors.New("the release publishes no checksum for this download, refusing to install it unverified")
}

func Fetch(httpc *http.Client, name, url, sum, cacheDir string) (string, error) {
	if httpc == nil {
		httpc = http.DefaultClient
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(cacheDir, name)
	if got, err := fileSum(dest); err == nil && got == sum {
		return dest, nil
	}
	resp, err := httpc.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: HTTP %d", name, resp.StatusCode)
	}
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		os.Remove(tmp)
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", name, sum, got)
	}
	return dest, os.Rename(tmp, dest)
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Extract(archive, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if strings.HasSuffix(archive, ".zip") {
		return extractZip(archive, dest)
	}
	return extractTar(archive, dest)
}

func safeJoin(dest, name string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(name))
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return target, nil
}

func writeFile(path string, mode os.FileMode, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func extractTar(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			err = writeFile(target, os.FileMode(hdr.Mode), tr)
		case tar.TypeSymlink:
			if filepath.IsAbs(hdr.Linkname) || strings.HasPrefix(hdr.Linkname, "/") || strings.HasPrefix(hdr.Linkname, `\`) {
				return fmt.Errorf("archive link %q points outside the destination", hdr.Name)
			}
			if _, lerr := safeJoin(dest, filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)); lerr != nil {
				return lerr
			}
			if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
				err = os.Symlink(hdr.Linkname, target)
			}
		}
		if err != nil {
			return err
		}
	}
}

func extractZip(archive, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target, err := safeJoin(dest, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, zf.Mode(), rc)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
