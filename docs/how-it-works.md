# How it works

`runnerkit add owner/repo` does these things:

1. Checks that the repository is private and that no runner with that name is registered
   on GitHub yet (looked up by name), so it never takes over another machine's runner by
   accident.
2. Downloads the latest official `actions/runner` for this operating system and
   architecture, checks its SHA-256 and unpacks it into the runnerkit data folder.
3. Asks GitHub for a registration token and runs the runner's own `config.sh`
   (`config.cmd` on Windows). The token reaches it through the environment, not the command
   line, so other users of the machine cannot read it from the process list.
4. Writes a user-level service that starts `runnerkit supervise` for this runner.
5. Starts it and prints the `runs-on` line to put in your workflow.

If saving the runner or installing its service fails after the registration, runnerkit
unregisters the runner again so GitHub is not left with an offline runner. Nothing is installed system-wide and nothing needs
administrator rights.

## Where things live

| | Linux | macOS | Windows |
|---|---|---|---|
| Data folder | `~/.local/share/runnerkit` (or `$XDG_DATA_HOME/runnerkit`) | `~/Library/Application Support/runnerkit` | `%LOCALAPPDATA%\runnerkit` |
| Service | systemd user unit | launchd agent | Task Scheduler task |
| Runs | while the user manager runs (use `enable-linger` to survive logout) | while you are logged in | while you are logged in |

Set `RUNNERKIT_HOME` to move the data folder; the service remembers the folder it was
created with. Each runner has its own folder under `runners/`, its log under `logs/`, and
the list of runners is `runners.json`.

## The supervisor

The service does not start the runner directly. It starts `runnerkit supervise`, which
behaves the same on all three systems and takes care of what the service managers do not:

- It restarts the runner if it exits, with a growing delay when it fails repeatedly.
- With battery pausing on (the default), it waits for power before starting, and stops the
  runner when the machine is on battery and no job is running. A job in progress is never
  interrupted by the battery check.
- On Linux, when the service is stopped it waits for a job in progress to finish, up to 45
  minutes, before stopping the runner. GitHub's runner has no drain mode, so a new job can
  still be picked up during that wait. macOS ends the process shortly after asking it to
  stop and Windows ends it at once; on both, the supervisor stops the runner without waiting
  for the job. For that reason `runnerkit stop`, `restart` and `remove` refuse on every
  system while a job is running; `--force` stops it without waiting for the job.
- Before each start it deletes work folders with no change in the last 14 days (it looks at
  the folder and the two levels below it) and diagnostic logs older than 7 days, and always
  empties `_work/_temp`. `_tool`, `_actions` and `_PipelineMapping` are never deleted and
  symbolic links are never followed. `runnerkit clean` does the same by
  hand and skips a runner that has a job running.
- The runner is told to forward stop signals to its listener, so stopping it leaves no
  stray processes. If it does not exit within 30 seconds (8 on macOS and Windows) its whole
  process group is killed.
  On Windows the supervisor puts itself in a job object, so the system also ends the runner
  when the supervisor is ended, and it hides its console window.
- Jobs do not inherit `GH_TOKEN`, `GITHUB_TOKEN` or `RUNNERKIT_TOKEN`, even if the service
  was started from a shell that had them.
- It writes the runner output to `logs/<name>.log`. At 5 MB the log is moved to
  `<name>.log.1`, replacing the previous one.

## Labels

Every runner gets GitHub's default labels (`self-hosted`, the operating system and the
architecture) plus `runnerkit` and any you pass with `--labels`. A workflow picks it with
`runs-on: [self-hosted, linux, x64, runnerkit]`.

## Limits of version 1

- GitHub.com only. GitHub Enterprise Server and Forgejo or Gitea runners are not supported yet.
- One job at a time per runner. Add a second runner with another `--name` to run two.
- Runners are not ephemeral: the work folder persists between jobs, as with any runner you
  install by hand.
- Job detection looks for the runner's own worker process, by its folder.
- On macOS the service needs a graphical login session. Over SSH on a headless Mac,
  log in on the screen once and run `runnerkit start NAME`.
