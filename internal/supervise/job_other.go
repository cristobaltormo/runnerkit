//go:build !windows

package supervise

func TieChildrenToSupervisor() error { return nil }

func HideConsole() {}
