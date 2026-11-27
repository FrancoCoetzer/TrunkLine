# CLAUDE.md

Guidance for Claude Code in this repo. The README covers what the app does and
how to build it; this file is what to keep in mind while changing it.

## What this is

TrunkLine: an open-source (GPL v3) Go + Wails v2 desktop SVN client for Linux
and Windows. The frontend is plain HTML/JS in `frontend/dist` with no build
step; the Makefile stamps the version.
The page's scripts are `frontend/dist/js/*.js`, one per area, loaded in order by
`index.html` as classic scripts sharing one global scope (not modules): code
that runs while a file loads may only use what earlier files define.

## Build and test

```sh
make         # go test ./..., then build/bin/trunkline
make test    # needs svn and svnadmin: tests create real repositories in temp dirs
make vet     # Linux and Windows
make ui-test # Playwright against the dev build (tests/ui; needs Node.js)
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
  JSON (`appError`); `call()` (`js/core.js`) shows a login or trust dialog for
  `auth`/`cert` and retries. A new prompt-like failure gets a Kind there.
- **svn's messages are forced to English** (`environ()`), but the character
  set is kept, so non-ASCII file names still work. Don't set `LC_ALL=C`.
- **Windows**: svn.exe runs with no console window (`exec_windows.go`). The
  app is cross-built, so check both with `make vet`.
- Change paths sent between the page and Go are relative to the working copy
  root, with `/`. `app.go` joins them onto the root.
- Passwords are never written to disk by the app (`internal/config` stores
  usernames only).
- **Dialogs**: `ask()` in `js/core.js` resolves on the form's submit and the
  dialog's cancel, not the `close` event (a webview that isn't painting can
  hold that back). Keep it that way.
- This is a public repo: no company names, internal hostnames or paths.
- **The Windows pictures** (`packaging/windows/trunkline.ico`, the installer's
  `installer-side.bmp` and `installer-header.bmp`) are committed and drawn by
  `packaging/windows/make_icon.py`, which mirrors the geometry of
  `packaging/trunkline.svg`. Change the SVG and the script together, then
  `make winres`.
- **The .exe's resources** (icon group 3, which Wails loads; version info;
  the manifest) are written for each Windows build by
  `packaging/windows/make_syso.py` into `rsrc_windows_<arch>.syso`, which git
  ignores. Every Windows build in the Makefile runs it first.
- **svn.exe on Windows writes paths in non-XML output in the ANSI code page**
  (diff headers, svn patch's report), and svn patch reads them that way.
  `utf8Headers`/`fromNative` and `nativeHeaders`/`toNative`
  (`internal/svn/diffs.go`, `native_*.go`) convert; patches TrunkLine writes
  are UTF-8. `--xml` output is always UTF-8.
- **The .msi** (`packaging/windows/trunkline.wxs`) is built on Linux by
  `wixl`, which only knows part of WiX: check a change with `msiinfo export`
  and on Windows. It doesn't support HKMU registry values (use a file as a
  component's key path) and leaves file versions empty (so
  `REINSTALLMODE=amus`, or upgrades leave an empty folder).

- **Command line** (`cli.go`): `trunkline <action> <paths>`, which the file
  manager menus will run too. Each action in `cliActions` has a case in
  `runAction` (`js/start.js`); add both together. Actions end where the user would
  have got to by hand and never skip a confirmation (revert asks, commit waits
  for the message). One window: a second launch hands its arguments over
  (`SingleInstanceLock`); dev builds use their own `instanceID`.

- **Updates** (`internal/update`): releases come from GitHub (`update.Repo`).
  An update is only installed after its SHA-256 matches the release's
  `SHA256SUMS`; keep that check. The asset names in `AssetName` (per install
  kind and processor) and in `packaging/dist.sh` must match, and the x64
  names must stay as they are: older installed copies look for them. A
  build's version and install kind (deb, rpm, windows, msi, source) are
  stamped by `packaging/dist.sh` and the Makefile; a plain `go build` has
  neither and never offers updates.
- **Signing** (Windows): `packaging/windows/sign.sh` signs each program, the
  setup (and its uninstaller, through NSIS's `!finalize`/`!uninstfinalize`)
  and the .msi, when signing is set up (`SIGN_PFX` for osslsigncode, or
  `SIGN_JSIGN` for jsign and a cloud key); otherwise it does nothing. The
  release workflow sets it up from repository secrets.
- **Flatpak and snap**: `packaging/flatpak/` (built by `dist.sh flatpak`,
  svn 1.14 built in from source) and `snap/snapcraft.yaml` (svn from
  Ubuntu's package). Both are stamped with their install kind; the updater
  only tells their users a release exists.
- **Processors**: Linux packages are built natively on x64 and ARM64 (cgo:
  GTK and WebKit can't be cross-built); Windows is cross-built for both. The
  release workflow runs the Linux half on GitHub's ARM64 runner.
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
