package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/clean"
	"github.com/cristobaltormo/runnerkit/internal/store"
	"github.com/cristobaltormo/runnerkit/internal/supervise"
)

func (a *App) cmdList(args []string) error {
	runners, err := a.store().List()
	if err != nil {
		return err
	}
	if len(runners) == 0 {
		fmt.Fprintln(a.Out, "No runners on this machine. Add one with: runnerkit add <owner/repo>")
		return nil
	}
	tw := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSCOPE\tSTATE\tLABELS")
	for _, r := range runners {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Scope, a.Service.Status(r.Name), strings.Join(r.Labels, ","))
	}
	return tw.Flush()
}

func (a *App) cmdStatus(args []string) error {
	fs := a.flags("status")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	runners, err := a.store().List()
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		if _, err := a.store().Get(pos[0]); err != nil {
			return err
		}
	}
	shown := 0
	for _, r := range runners {
		if len(pos) > 0 && pos[0] != r.Name {
			continue
		}
		shown++
		fmt.Fprintf(a.Out, "%s\n  scope    %s\n  state    %s\n  runner   %s\n  folder   %s\n  battery  %s\n  log      %s\n",
			r.Name, r.Scope, a.Service.Status(r.Name), r.Version, r.Dir, batteryPolicy(r), a.logPath(r.Name))
	}
	if shown == 0 {
		fmt.Fprintln(a.Out, "No runners on this machine.")
	}
	return nil
}

func batteryPolicy(r store.Runner) string {
	if r.PauseOnBattery {
		return "pauses when idle on battery"
	}
	return "keeps running"
}

func (a *App) cmdControl(action string, args []string) error {
	fs := a.flags(action)
	force := fs.Bool("force", false, "stop even if a job is running")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: runnerkit %s <name>", action)
	}
	r, err := a.store().Get(pos[0])
	if err != nil {
		return err
	}
	if action != "start" && !*force && a.Probe.Busy(r.Dir, 0) {
		return fmt.Errorf("a job is running on %s; wait for it to finish or use --force", r.Name)
	}
	switch action {
	case "start":
		err = a.Service.Start(r.Name)
	case "stop":
		err = a.Service.Stop(r.Name)
	case "restart":
		_ = a.Service.Stop(r.Name)
		a.waitStopped(r.Name)
		err = a.Service.Start(r.Name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "%s: %s done\n", r.Name, action)
	return nil
}

func (a *App) waitStopped(name string) {
	for i := 0; i < 30 && a.Service.Status(name) == "running"; i++ {
		time.Sleep(500 * time.Millisecond)
	}
}

func (a *App) cmdLogs(args []string) error {
	fs := a.flags("logs")
	lines := fs.Int("n", 50, "number of lines")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || *lines < 1 {
		return fmt.Errorf("usage: runnerkit logs <name> [-n lines], with at least one line")
	}
	if _, err := a.store().Get(pos[0]); err != nil {
		return err
	}
	f, err := os.Open(a.logPath(pos[0]))
	if err != nil {
		return fmt.Errorf("no log yet: %w", err)
	}
	defer f.Close()
	ring := make([]string, 0, *lines)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if len(ring) == *lines {
			ring = ring[1:]
		}
		ring = append(ring, sc.Text())
	}
	for _, l := range ring {
		fmt.Fprintln(a.Out, l)
	}
	return sc.Err()
}

func (a *App) cmdClean(args []string) error {
	fs := a.flags("clean")
	days := fs.Int("days", 14, "delete work folders untouched for this many days")
	dry := fs.Bool("dry-run", false, "only show what would be deleted")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	runners, err := a.store().List()
	if err != nil {
		return err
	}
	var total int64
	for _, r := range runners {
		if len(pos) > 0 && pos[0] != r.Name {
			continue
		}
		if a.Probe.Busy(r.Dir, 0) {
			fmt.Fprintf(a.Out, "%s: a job is running, skipped\n", r.Name)
			continue
		}
		res, err := clean.Run(r.Dir, time.Duration(*days)*24*time.Hour, *dry, time.Now())
		if err != nil {
			return err
		}
		total += res.Bytes
		verb := "freed"
		if *dry {
			verb = "would free"
		}
		fmt.Fprintf(a.Out, "%s: %d items, %s %s\n", r.Name, len(res.Removed), verb, clean.Human(res.Bytes))
	}
	return nil
}

func (a *App) cmdRemove(args []string) error {
	fs := a.flags("remove")
	token := fs.String("token", "", "GitHub token")
	force := fs.Bool("force", false, "delete locally even if GitHub cannot be reached")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: runnerkit remove <name> [--force]")
	}
	r, err := a.store().Get(pos[0])
	if err != nil {
		return err
	}
	if err := a.Service.Stop(r.Name); err != nil {
		fmt.Fprintf(a.Err, "note: could not stop %s: %v\n", r.Name, err)
	}
	remote := func() error {
		tok, err := a.Token(*token)
		if err != nil {
			return err
		}
		rm, err := a.client(tok).RemovalToken(r.Scope)
		if err != nil {
			return err
		}
		if err := a.Exec(r.Dir, []string{"ACTIONS_RUNNER_INPUT_TOKEN=" + rm}, filepath.Join(r.Dir, a.configScript()), "remove"); err != nil {
			return redact(err, rm)
		}
		return nil
	}
	if err := remote(); err != nil {
		if !*force {
			return fmt.Errorf("could not unregister the runner from GitHub: %w\nRun again with --force to delete it locally anyway and remove it by hand in the repository settings", err)
		}
		fmt.Fprintf(a.Err, "warning: not unregistered from GitHub (%v). Delete %s in the %s settings.\n", err, r.Name, r.Scope)
	}
	if err := a.Service.Remove(r.Name); err != nil {
		fmt.Fprintf(a.Err, "warning: removing the service: %v\n", err)
	}
	if err := os.RemoveAll(r.Dir); err != nil {
		return err
	}
	os.Remove(a.logPath(r.Name))
	os.Remove(a.logPath(r.Name) + ".1")
	if err := a.store().Delete(r.Name); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Runner %s removed\n", r.Name)
	return nil
}

func (a *App) cmdSupervise(args []string) error {
	fs := a.flags("supervise")
	home := fs.String("home", "", "runnerkit data folder")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: runnerkit supervise [--home dir] <name>")
	}
	if a.IsRoot() {
		return fmt.Errorf("refusing to run a runner as root")
	}
	if *home != "" {
		a.DataDir = *home
	}
	r, err := a.store().Get(pos[0])
	if err != nil {
		return err
	}
	logFile, err := supervise.OpenLog(a.logPath(r.Name))
	if err != nil {
		return err
	}
	defer logFile.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return supervise.Run(ctx, supervise.Config{
		Name:           r.Name,
		Dir:            r.Dir,
		PauseOnBattery: r.PauseOnBattery,
		Probe:          a.Probe,
	}, logFile)
}
