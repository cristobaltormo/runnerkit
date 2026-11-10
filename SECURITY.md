# Security

runnerkit installs software that executes the code of your workflows, and it handles
GitHub tokens, so a problem in it matters. Please report vulnerabilities privately
through GitHub's security advisories for this repository instead of opening a public
issue. You can expect an answer within a few days.

Things worth knowing:

- Tokens are read from a flag, the environment or `gh`, used only while a command runs and
  never written to disk by runnerkit. The runner itself stores its own credentials in its folder.
- The downloaded runner is verified against the SHA-256 that GitHub publishes with the
  release before it is unpacked. If no checksum is available, runnerkit refuses to install.
- Whatever your workflows run, runs as your user. Read [docs/security.md](docs/security.md)
  before pointing a runner at a repository that accepts outside contributions.
