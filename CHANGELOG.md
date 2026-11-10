# Changelog

## Unreleased

- Forgejo, Gitea and GitLab runners, chosen from the address of the repository, with the same commands as GitHub.
- First version: `add`, `list`, `status`, `start`, `stop`, `restart`, `logs`, `clean`, `remove` and `doctor` on Linux, macOS and Windows.
- Runners run as the current user, never as root, under systemd user services, launchd agents or Task Scheduler.
- Runners pause on battery when idle, and clean old work folders and logs.
- The default runner name keeps the repository part and is shortened from the host name.
- A runner name already registered on GitHub is refused unless `--replace` is given.
- On GitHub the registration token goes through the environment, and jobs do not inherit any forge token.
