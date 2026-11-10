package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/cristobaltormo/runnerkit/internal/clean"
	"github.com/cristobaltormo/runnerkit/internal/forge"
	"github.com/cristobaltormo/runnerkit/internal/store"
	"github.com/cristobaltormo/runnerkit/internal/supervise"
	"github.com/cristobaltormo/runnerkit/internal/sys"
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
	fmt.Fprintln(tw, "NAME\tFORGE\tSCOPE\tSTATE\tLABELS")
	for _, r := range runners {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Name, kindOf(r), r.Scope, a.Service.Status(r.Name), strings.Join(r.Labels, ","))
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
		fmt.Fprintf(a.Out, "%s\n  forge    %s\n  scope    %s\n  state    %s\n  runner   %s\n  folder   %s\n  battery  %s\n  log      %s\n",
			r.Name, kindOf(r), r.Scope, a.Service.Status(r.Name), r.Version, r.Dir, batteryPolicy(r), a.logPath(r.Name))
	}
	if shown == 0 {
		fmt.Fprintln(a.Out, "No runners on this machine.")
	}
	return nil
}

func kindOf(r store.Runner) forge.Kind {
	if r.Forge == "" {
		return forge.GitHub
	}
	return forge.Kind(r.Forge)
}

func styleOf(r store.Runner) sys.Style {
	if kindOf(r) == forge.GitHub {
		return sys.ByWorker
	}
	return sys.ByChildren
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
	if action != "start" && !*force && a.Probe.Busy(r.Dir, 0, styleOf(r)) {
		return fmt.Errorf("a job is running on %s; wait for it to finish or use --force", r.Name)
	}
	stop := a.Service.Stop
	if *force {
		stop = a.Service.StopNow
	}
	switch action {
	case "start":
		err = a.Service.Start(r.Name)
	case "stop":
		err = stop(r.Name)
	case "restart":
		_ = stop(r.Name)
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
		if a.Probe.Busy(r.Dir, 0, styleOf(r)) {
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
	force := fs.Bool("force", false, "delete even if a job is running or the server cannot be reached")
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
	if !*force && a.Probe.Busy(r.Dir, 0, styleOf(r)) {
		return fmt.Errorf("a job is running on %s; wait for it to finish or use --force", r.Name)
	}
	if err := a.Service.StopNow(r.Name); err != nil {
		fmt.Fprintf(a.Err, "note: could not stop %s: %v\n", r.Name, err)
	}
	remote := func() error {
		tok, err := a.Token(kindOf(r), *token)
		if err != nil {
			return err
		}
		p, err := a.provider(kindOf(r), r.URL, tok)
		if err != nil {
			return err
		}
		return p.Deregister(forge.Record{Dir: r.Dir, Scope: r.Scope, Name: r.Name, RunnerID: r.RunnerID, GOOS: a.GOOS, Exec: forge.Exec(a.Exec)})
	}
	if err := remote(); err != nil {
		if !*force {
			return fmt.Errorf("could not unregister the runner from the server: %w\nRun again with --force to delete it locally anyway and remove it by hand in the repository settings", err)
		}
		fmt.Fprintf(a.Err, "warning: not unregistered from the server (%v). Delete %s in the settings of %s.\n", err, r.Name, r.Scope)
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
	supervise.HideConsole()
	if err := supervise.TieChildrenToSupervisor(); err != nil {
		fmt.Fprintf(logFile, "%s supervisor: the runner will outlive the supervisor if it is killed: %v\n", time.Now().Format(time.RFC3339), err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return supervise.Run(ctx, supervise.Config{
		Name:           r.Name,
		Dir:            r.Dir,
		PauseOnBattery: r.PauseOnBattery,
		Probe:          a.Probe,
		Style:          styleOf(r),
		Command:        supervise.Command(kindOf(r)),
	}, logFile)
}
