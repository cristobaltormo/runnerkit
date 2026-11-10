# Changelog

## Unreleased

- First version: `add`, `list`, `status`, `start`, `stop`, `restart`, `logs`, `clean`, `remove` and `doctor` for GitHub repositories and organizations on Linux, macOS and Windows.
- Runners run as the current user, never as root, under systemd user services, launchd agents or Task Scheduler.
- Runners pause on battery when idle, and clean old work folders and logs.
- The default runner name keeps the repository part and is shortened from the host name.
- A runner name already registered on GitHub is refused unless `--replace` is given.
- Registration tokens are passed through the environment and jobs do not inherit GitHub tokens.
