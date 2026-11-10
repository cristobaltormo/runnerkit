# Contributing

Bug reports and pull requests are welcome. For anything bigger than a fix, open an
issue first so we can agree on the approach.

## Building and testing

```
make test      # go vet and go test -race
make build
```

Go 1.24 or newer, and no third-party dependencies. The tests use fake servers and fake
runners and need no network. A change is not done until `make test` passes.

## Style

The code has no comments: names and small functions carry the meaning, and the
reasons for a decision go in the docs. Keep messages shown to users short and say what to
do next. No emojis anywhere.

## Pull requests

The GitHub repository may be a mirror of the one this project is developed in, so a commit
made directly on GitHub can be overwritten by the next sync. Pull requests are still
welcome: they are reviewed on GitHub, applied to the main repository with your authorship
kept, and closed with a note saying which commit contains the change.
