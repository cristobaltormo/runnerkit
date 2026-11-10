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
	entries, err := os.ReadDir(work)
	if err != nil && !os.IsNotExist(err) {
		return res, err
	}
	for _, e := range entries {
		path := filepath.Join(work, e.Name())
		if e.Name() == "_temp" {
			inner, _ := os.ReadDir(path)
			for _, i := range inner {
				if err := remove(filepath.Join(path, i.Name())); err != nil {
					return res, err
				}
			}
			continue
		}
		if keep[e.Name()] || !e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			if err := remove(path); err != nil {
				return res, err
			}
		}
	}

	diag := filepath.Join(runnerDir, "_diag")
	logs, _ := os.ReadDir(diag)
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
