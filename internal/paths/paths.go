package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func DataDir() (string, error) {
	if v := os.Getenv("RUNNERKIT_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find the home directory: %w", err)
	}
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "runnerkit"), nil
		}
		return filepath.Join(home, "AppData", "Local", "runnerkit"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "runnerkit"), nil
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "runnerkit"), nil
	}
	return filepath.Join(home, ".local", "share", "runnerkit"), nil
}
