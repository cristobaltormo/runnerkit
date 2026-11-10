package clean

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunDeletesOnlyOldWork(t *testing.T) {
	dir := t.TempDir()
	mk := func(rel string, age time.Duration) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, "f"), []byte("12345"), 0o644)
		old := time.Now().Add(-age)
		os.Chtimes(p, old, old)
	}
	mk("_work/old", 30*24*time.Hour)
	mk("_work/recent", time.Hour)
	mk("_work/_tool", 90*24*time.Hour)
	mk("_work/_temp/leftover", time.Hour)
	os.MkdirAll(filepath.Join(dir, "_diag"), 0o755)
	os.WriteFile(filepath.Join(dir, "_diag", "old.log"), []byte("x"), 0o644)
	oldTime := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(filepath.Join(dir, "_diag", "old.log"), oldTime, oldTime)
	os.WriteFile(filepath.Join(dir, "_diag", "new.log"), []byte("x"), 0o644)

	dry, err := Run(dir, 14*24*time.Hour, true, time.Now())
	if err != nil || len(dry.Removed) != 3 {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "_work", "old")); err != nil {
		t.Fatal("dry run must not delete")
	}
	res, err := Run(dir, 14*24*time.Hour, false, time.Now())
	if err != nil || len(res.Removed) != 3 || res.Bytes != 11 {
		t.Fatalf("real run: %+v %v", res, err)
	}
	for _, kept := range []string{"_work/recent", "_work/_tool", "_diag/new.log"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s must be kept", kept)
		}
	}
	for _, gone := range []string{"_work/old", "_work/_temp/leftover", "_diag/old.log"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s must be deleted", gone)
		}
	}
}

func TestHuman(t *testing.T) {
	if Human(512) != "512 B" || Human(2048) != "2.0 KiB" || Human(3<<30) != "3.0 GiB" {
		t.Fatal("unexpected formatting")
	}
}
