# Forges

runnerkit installs the runner that each forge publishes. The command line is the same; what
differs is the runner program, the token it needs and a few quirks, all listed here.

| Forge | Runner | Systems | Version tested |
|---|---|---|---|
| GitHub | `actions/runner` | Linux, macOS, Windows | 2.338 |
| Forgejo | `forgejo-runner` | Linux | Forgejo 16.0.7, runner 13.2.1 |
| Gitea | `gitea-runner` | Linux, macOS, Windows | Gitea 1.27.3, runner 5.4.1 |
| GitLab | `gitlab-runner` | Linux, macOS, Windows | GitLab CE 19.5.4, runner 19.5.3 |

Tested means a real server, a real runner and a real job: `runnerkit add`, the runner shows up
online, a job runs to success, `stop` and `clean` refuse while a job runs, `stop --force`,
`restart` and `remove` leave nothing behind (the server forgets the runner, no process, no
service, no folder). The test servers ran in Docker. Where each combination was tested:

| | Linux | macOS | Windows |
|---|---|---|---|
| GitHub | yes | yes | yes |
| Forgejo | yes | no runner published | no runner published |
| Gitea | yes | yes | unit tests only |
| GitLab | yes | yes | unit tests only |

## Addresses and tokens

`https` is expected. Plain `http` is accepted for `localhost`, private network addresses and
single-label or `.lan`/`.local` names; for anything else `runnerkit add` stops unless you pass
`--allow-http`. If the server answers a request with a redirect to another host, runnerkit
stops instead of following it, so the token never leaves the server you named.

## How the forge is chosen

`runnerkit add owner/repo` means GitHub. For anything else give the address of the repository:
`runnerkit add https://git.example.com/owner/repo`. runnerkit asks the server which software it
runs. Pass `--forge github|forgejo|gitea|gitlab` when the server hides that. Servers under a
sub-path (`https://example.com/git/`) are not supported.

## GitHub

Token: a fine-grained token with administration write access to the repository, or a classic
token with the `repo` scope; for an organization, the self-hosted runners permission. Labels
are the default ones plus `runnerkit`. Workflow line: `runs-on: [self-hosted, linux, x64, runnerkit]`.
Organization runners and `--group` are supported but were not tested.

## Forgejo and Gitea

Token: an access token with write access to the repository (`write:repository`; Forgejo also
accepts read). Actions must be enabled on the instance and for the repository.

Labels are `runnerkit`, the system, the architecture and your own, all in host mode, which
means jobs run directly on your machine. Workflow line: `runs-on: [runnerkit, linux, x64]`
(`macos` or `windows` on those systems).

Quirks seen on the test servers:

- Forgejo hands out the registration token with GET, Gitea with POST. runnerkit tries the right
  one first and falls back to the other.
- Forgejo lists runners as a plain array, Gitea as an object. Both are read.
- Forgejo publishes its runner for Linux (amd64 and arm64) only. On macOS or Windows build it and
  pass `--runner-binary /path/to/forgejo-runner`. The Gitea runner is published for all three.
- Gitea renamed `act_runner` to `gitea/runner`; runnerkit uses the new name.

## GitLab

Token: a personal access token with the `api` scope (or `create_runner` and `manage_runner`)
and Maintainer access to the project. runnerkit creates a project runner through the API and
registers it with the shell executor. Tags are `runnerkit`, the system, the architecture and
your own, and the runner does not take untagged jobs. Workflow line:
`tags: [runnerkit, linux, x64]`.

Quirks seen on the test servers:

- On Debian and Ubuntu the default `~/.bash_logout` clears the console, which makes every shell
  job fail with `prepare environment: exit status 1`. Delete that file (it is only a screen
  clear) or remove its last lines. `runnerkit add` warns about it.
- GitLab publishes one checksum list for the latest runner, so the version shows as `latest`.
- Project runners only; group and instance runners are not implemented.

## macOS and the local network

macOS blocks background processes from reaching machines on your local network unless they are
allowed in System Settings, Privacy and Security, Local Network. The runner itself usually gets
through, but `git` started by a job may not: a GitLab job against `http://192.168.x.x` failed
at the clone with "Couldn't connect to server" while the same server was reachable from a
terminal. Use a server address that is not a local network address, or allow the programs in
that setting. A job that does not clone (`GIT_STRATEGY: none`) is not affected.

## Gogs

Gogs has no built-in CI and no runners, so `runnerkit add` explains that and stops. Use it
together with a CI that Gogs can call, or move to Forgejo or Gitea.

## Others

Bitbucket, OneDev, GitBucket and Azure DevOps are not supported. Adding a forge means a type
in `internal/forge` that implements `forge.Provider`: scope check, privacy check, runner name
lookup, download with checksum, registration and deregistration.
