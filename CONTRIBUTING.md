# Contributing to TrunkLine

Thanks for helping. Bug reports, ideas and code are all welcome.

## Reporting a problem

Use **Help, Report a problem…** in the app, or
[open an issue](https://github.com/FrancoCoetzer/TrunkLine/issues/new/choose).
Say which TrunkLine version you have (Help, About), your OS, and your svn
version (`svn --version --quiet`), and paste svn's message if there was one.

Questions and "how do I…?" go in
[Discussions](https://github.com/FrancoCoetzer/TrunkLine/discussions).

Security problems: please don't open a public issue. See [SECURITY.md](SECURITY.md).

## Suggesting a feature

Open a feature request and describe what you're trying to do, not only the
button you want. If you're coming from TortoiseSVN or another client, saying
how it worked there helps.

## Building and testing

You need Go, the svn command-line client and `svnadmin`, and on Linux
`libgtk-3-dev` and `libwebkit2gtk-4.1-dev`.

```sh
make            # tests, then build/bin/trunkline
make run        # build and start it
make test       # go test ./... (creates real svn repositories in temp folders)
make vet        # vet for Linux and Windows
make dev        # debug build with Inspect, also served on http://localhost:34115
```

Always build with `make`: it stamps the version. The frontend
(`frontend/dist`) has no build step, but it's embedded in the binary, so
rebuild after editing it.

## Making a change

1. Open an issue first for anything bigger than a small fix, so we can agree
   on the approach before you spend time on it.
2. Fork the repository and make a branch from `main`.
3. Keep each pull request to one change, with a short commit message that
   says what it does.
4. Run `make test` and `make vet` before you push. CI runs the tests on Linux
   and Windows, x64 and ARM64.
5. If the change touches Windows behaviour (paths, encodings, svn.exe), say
   whether you tested it on Windows.

## How the code works

These rules keep TrunkLine behaving like the svn command line:

- **All svn work goes through the `svn` binary** (`internal/svn`), not a
  reimplementation or libsvn bindings. Parse `--xml` output where svn has it
  (status, info, log, list), not the human-readable text.
- **Every svn call is `--non-interactive`.** svn must never wait on a prompt.
  Passwords go on stdin (`--password-from-stdin`), never as an argument, and
  the app never writes them to disk.
- **Errors are classified by svn's error codes** (`E170001`, …), not by
  message text (`newError` in `internal/svn/svn.go`).
- **Paths between the page and Go** are relative to the working copy root,
  with `/` separators.
- **The update check** must keep verifying each download's SHA-256 against
  the release's `SHA256SUMS` (`internal/update`).

`CLAUDE.md` has more detail on Windows encodings, packaging and releases.

## License

TrunkLine is GPL v3. By contributing, you agree your contribution is
licensed under the same terms.
