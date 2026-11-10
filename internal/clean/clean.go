package clean

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Result struct {
	Removed []string
	Bytes   int64
}

var keep = map[string]bool{"_tool": true, "_actions": true, "_PipelineMapping": true}

func Run(runnerDir string, olderThan time.Duration, dryRun bool, now time.Time) (Result, error) {
	var res Result
	cutoff := now.Add(-olderThan)

	remove := func(path string) error {
		size := treeSize(path)
		res.Removed = append(res.Removed, path)
		res.Bytes += size
		if dryRun {
			return nil
		}
		return os.RemoveAll(path)
	}

	work := filepath.Join(runnerDir, "_work")
	var entries []os.DirEntry
	if realDir(work) {
		var err error
		entries, err = os.ReadDir(work)
		if err != nil {
			return res, err
		}
	}
	for _, e := range entries {
		path := filepath.Join(work, e.Name())
		if !e.IsDir() {
			continue
		}
		if e.Name() == "_temp" {
			inner, _ := os.ReadDir(path)
			for _, i := range inner {
				if err := remove(filepath.Join(path, i.Name())); err != nil {
					return res, err
				}
			}
			continue
		}
		if keep[e.Name()] {
			continue
		}
		if newestChange(path).Before(cutoff) {
			if err := remove(path); err != nil {
				return res, err
			}
		}
	}

	diag := filepath.Join(runnerDir, "_diag")
	var logs []os.DirEntry
	if realDir(diag) {
		logs, _ = os.ReadDir(diag)
	}
	logCutoff := now.Add(-7 * 24 * time.Hour)
	for _, e := range logs {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(logCutoff) {
			if err := remove(filepath.Join(diag, e.Name())); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

func realDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

func newestChange(dir string) time.Time {
	now := time.Now()
	var newest time.Time
	note := func(path string) bool {
		info, err := os.Lstat(path)
		if err != nil {
			return false
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return true
	}
	if !note(dir) {
		return now
	}
	children, err := os.ReadDir(dir)
	if err != nil {
		return now
	}
	for _, c := range children {
		child := filepath.Join(dir, c.Name())
		if !note(child) {
			return now
		}
		if c.IsDir() {
			grand, err := os.ReadDir(child)
			if err != nil {
				return now
			}
			for _, g := range grand {
				if !note(filepath.Join(child, g.Name())) {
					return now
				}
			}
		}
	}
	return newest
}

func treeSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func Human(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
