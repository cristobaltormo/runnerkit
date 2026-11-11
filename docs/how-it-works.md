# How it works

`runnerkit add owner/repo` (or `runnerkit add https://server/owner/repo` for Forgejo, Gitea and
GitLab) does these things. Each forge is a provider in `internal/forge`; the steps are the same
and only the runner and its API change (see [providers.md](providers.md)).

1. Works out which forge it is (the answers of the server must look like that forge; an
   address that redirects to another host is refused so the token is never sent there), checks
   that the repository is private and that no runner with
   that name is registered yet, so it never takes over another machine's runner by accident.
2. Downloads the latest official runner of that forge for this operating system and
   architecture, checks its SHA-256 and puts it in the runnerkit data folder.
3. Registers it. On GitHub it asks for a registration token and runs the runner's own
   `config.sh` (`config.cmd` on Windows), passing the token through the environment so other
   users of the machine cannot read it from the process list. Gitea gives a registration token
   that goes to `register` through `GITEA_RUNNER_REGISTRATION_TOKEN`; GitLab creates a runner
   through its API and the token it returns goes to `register` through `CI_SERVER_TOKEN`.
   Forgejo's runner only accepts its token as an argument, so on a shared machine that token
   is visible in the process list for the second the `register` call lasts.
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
- Jobs do not inherit `GH_TOKEN`, `GITHUB_TOKEN`, `GITLAB_TOKEN`, `FORGEJO_TOKEN`,
  `GITEA_TOKEN` or `RUNNERKIT_TOKEN`, even if the service was started from a shell that had them.
- It writes the runner output to `logs/<name>.log`. At 5 MB the log is moved to
  `<name>.log.1`, replacing the previous one.

## Labels

On GitHub every runner gets the default labels (`self-hosted`, the operating system and the
architecture) plus `runnerkit` and any you pass with `--labels`; a workflow picks it with
`runs-on: [self-hosted, linux, x64, runnerkit]`. On Forgejo and Gitea the labels are
`runnerkit`, the system, the architecture and yours, in host mode
(`runs-on: [runnerkit, linux, x64]`). On GitLab they are the same as tags
(`tags: [runnerkit, linux, x64]`).

## Limits of version 1

- GitHub.com, and Forgejo, Gitea and GitLab by repository. GitHub Enterprise Server, servers under
  a sub-path, and organization or group runners on the other forges are not supported.
- One job at a time per runner. Add a second runner with another `--name` to run two.
- Runners are not ephemeral: the work folder persists between jobs, as with any runner you
  install by hand.
- Job detection: on GitHub it looks for the runner's `Runner.Worker` process, by its folder.
  Forgejo, Gitea and GitLab run jobs as child processes of the runner, so a runner is busy
  when its program has any child process.
- On macOS the service needs a graphical login session. Over SSH on a headless Mac,
  log in on the screen once and run `runnerkit start NAME`.
