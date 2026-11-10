# runnerkit

English | [Español](README.es.md)

[![CI](https://github.com/cristobaltormo/runnerkit/actions/workflows/ci.yml/badge.svg)](https://github.com/cristobaltormo/runnerkit/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

Run your GitHub Actions on your own computer instead of spending GitHub's minutes.

runnerkit installs the official GitHub self-hosted runner on Linux, macOS or Windows with
one command and runs it as a background service. It restarts the runner when it fails,
pauses it when your laptop is on battery, cleans old build folders and refuses to run as
root. Your workflows stay as they are, except for one line.

```
runnerkit add my-user/my-private-repo
```

```
Runner laptop-my-user-my-private-repo is installed and running.
To use it, put this in your workflow:

  runs-on: [self-hosted, linux, x64, runnerkit]
```

## Why

Private repositories get a limited number of free minutes on GitHub-hosted runners, and
macOS and Windows minutes count for more than Linux ones. Jobs that run on a self-hosted
runner do not use those minutes. The official runner is not hard to install, but running
it as a service on each system, keeping it tidy and keeping it safe takes some work.
runnerkit does that setup for you.

As of this writing GitHub does not charge for self-hosted runner minutes. GitHub announced
a per-minute fee in December 2025 and postponed it; check GitHub's billing page before
relying on this.

## Install

Download the binary for your system from the releases page and put it on your PATH. It is a
single static file with no dependencies.

| System | File |
|---|---|
| Linux | `runnerkit-linux-amd64`, `runnerkit-linux-arm64` |
| macOS | `runnerkit-darwin-arm64`, `runnerkit-darwin-amd64` |
| Windows | `runnerkit-windows-amd64.exe`, `runnerkit-windows-arm64.exe` |

Or build it with `make build` (Go 1.24 or newer).

## Use

First check the machine:

```
runnerkit doctor
```

You need a GitHub token with administration write access to the repository. The easiest way
is the GitHub CLI: run `gh auth login` once and runnerkit picks the token up. runnerkit looks
for a token in `--token`, then `RUNNERKIT_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN` and finally `gh`.
It is only used while the command runs and is never written to disk by runnerkit.

```
runnerkit add owner/repo                 one repository
runnerkit add my-org --group Default     an organization, optionally in a runner group
runnerkit add owner/repo --labels gpu    extra labels
runnerkit list                           runners on this machine and their state
runnerkit status NAME                    details of one
runnerkit logs NAME -n 100               end of its log
runnerkit stop NAME                      also start, restart; --force stops even with a job running
runnerkit clean --dry-run                what old build folders would be deleted
runnerkit remove NAME                    unregister from GitHub and delete everything
```

Other flags of `add`: `--name`, `--no-start`, `--no-pause-on-battery`, `--allow-public` and
`--replace` (take over a runner of the same name that is already registered).
`clean --days N` changes how old a work folder must be. `remove --force` deletes locally
when GitHub cannot be reached.

On Linux, run `loginctl enable-linger $USER` once so the runner keeps working when you log
out. On macOS and Windows the runner starts when you log in and stops when you log out.

## What it does

- Installs the official runner and checks its SHA-256 first; it refuses to continue without one.
- Runs it as a user-level service: systemd on Linux, launchd on macOS, Task Scheduler on
  Windows. No administrator rights.
- Restarts it when it fails, with a growing delay.
- Pauses it on battery when no job is running, and never interrupts a job because of the battery.
- Runs it at low priority so your computer stays usable.
- Deletes old work folders and logs.
- Keeps your GitHub token out of the jobs and off the command line of the runner setup.
- Refuses public repositories and refuses to run as root. See [docs/security.md](docs/security.md).

## Limits

GitHub.com only for now; support for Forgejo and Gitea runners is planned. One job at a time
per runner. Stopping the service waits for a running job to finish only on Linux; on macOS
and Windows the system ends the process. `runnerkit stop`, `restart` and `remove` refuse on
every system while a job runs unless you pass `--force`. The
macOS and Windows code is covered by tests of the files and commands it generates and has
had less real use than the Linux one. How everything works, and why, is in
[docs/how-it-works.md](docs/how-it-works.md).

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
