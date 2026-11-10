# Security

A self-hosted runner runs the code of your workflows on your machine, as your user. That
is the whole point, and also the risk.

## Public repositories

`runnerkit add` refuses public repositories. In a public repository anyone can open a pull
request from a fork, and a workflow triggered by it runs the contributor's code on your
computer. GitHub-hosted runners are free for public repositories, so there is no reason to
take that risk. `--allow-public` exists for the cases where you have restricted who can
trigger workflows and accept what is left.

For an organization runnerkit cannot see which repositories will use the runner. Put the
runner in a runner group that only private repositories can use and pass it with `--group`.

## What the runner can reach

The runner runs as the user who ran `runnerkit add`, so a workflow can read that user's files
and reach that machine's network. For repositories that are yours and private this is the same
trust you give any script you run. If that is too much:

- Use a dedicated user account for the runner.
- Run it on a spare machine or in a virtual machine.
- On a laptop, keep personal credentials out of the runner's user account.

runnerkit refuses to add a runner, or to run one, as root.

## Host mode on Forgejo, Gitea and GitLab

The runners installed for these forges run jobs directly on your machine, with the shell of your
user, not in a container (GitHub's runner does the same). Treat a repository served by them as
trusted code. If you want isolation, run the runner in a virtual machine or give it a
dedicated account.

## Tokens

- The token you give runnerkit needs permission to manage the runners of the repository. The
  exact scopes for each forge are in [providers.md](providers.md); on GitHub that is
  administration write access (a fine-grained token) or the `repo` scope (a classic token).
- runnerkit uses it only while a command runs and never stores it.
- The runner is registered with a short-lived registration token that the forge issues from
  your token. On GitHub it is handed to the runner through its environment; the other forges'
  runners only take it as an argument of the `register` call, which lasts a moment. It is
  removed from any error message.
- Jobs do not inherit `GH_TOKEN`, `GITHUB_TOKEN`, `GITLAB_TOKEN`, `FORGEJO_TOKEN`,
  `GITEA_TOKEN` or `RUNNERKIT_TOKEN`.
