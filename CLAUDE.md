# CLAUDE.md

Guidance for Claude Code in this repo. The README covers what the app does and
how to build it; this file is what to keep in mind while changing it.

## What this is

TrunkLine: an open-source (GPL v3) Go + Wails v2 desktop SVN client for Linux
and Windows. The frontend is plain HTML/JS in `frontend/dist` with no build
step; the Makefile stamps the version.

## Build and test

```sh
make         # go test ./..., then build/bin/trunkline
make test    # needs svn and svnadmin: tests create real repositories in temp dirs
make vet     # Linux and Windows
```

Always build with `make` (it stamps the version). The frontend is embedded:
rebuild after editing `frontend/dist`.

## Rules of the code

- **All svn work goes through the `svn` binary** (`internal/svn`), never a Go
  reimplementation or libsvn bindings. Use `--xml` output where svn has it
  (status, info, log, list) and parse that, not the human-readable text.
- **Every call is `--non-interactive`.** svn must never wait on a prompt.
  Passwords go on stdin with `--password-from-stdin`, never as an argument (they
  would show in the process list). Certificate trust is
  `--trust-server-cert-failures`, only for hosts the user trusted.
- **Errors are classified by svn's error codes** (`E170001`, ...), not by
  message text (`internal/svn/svn.go`, `newError`). `app.go` turns them into
  JSON (`appError`); `app.js`'s `call()` shows a login or trust dialog for
  `auth`/`cert` and retries. A new prompt-like failure gets a Kind there.
- **svn's messages are forced to English** (`environ()`), but the character
  set is kept, so non-ASCII file names still work. Don't set `LC_ALL=C`.
- **Windows**: svn.exe runs with no console window (`exec_windows.go`). The
  app is cross-built, so check both with `make vet`.
- Change paths sent between the page and Go are relative to the working copy
  root, with `/`. `app.go` joins them onto the root.
- Passwords are never written to disk by the app (`internal/config` stores
  usernames only).
- **Dialogs**: `ask()` in `app.js` resolves on the form's submit and the
  dialog's cancel, not the `close` event (a webview that isn't painting can
  hold that back). Keep it that way.
- This is a public repo: no company names, internal hostnames or paths.

- **Updates** (`internal/update`): releases come from GitHub (`update.Repo`).
  An update is only installed after its SHA-256 matches the release's
  `SHA256SUMS`; keep that check. The asset names in `AssetName` and in the
  Makefile's `dist` target must match. A build's version and install kind
  (deb, windows, source) are stamped by the Makefile; a plain `go build` has
  neither and never offers updates.
- **Releases**: `make release V=vX.Y.Z NOTES="..."` only when the user asks; it
  publishes to everyone who has the app installed. Don't leave a `make dist
  TAG=v9.9.9` package installed: it would never accept a real release.

## Testing by hand

`make dev` builds a debug binary and serves the page on http://localhost:34115
(with the Go methods), so a browser can drive it. Point it at a throwaway
repository (`svnadmin create`) and a scratch config with `XDG_CONFIG_HOME`,
not the user's real settings.

## Git

`main` is the only branch. Commit and push only when the user asks.
