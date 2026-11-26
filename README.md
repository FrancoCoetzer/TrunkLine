# TrunkLine

A modern desktop client for [Subversion (SVN)](https://subversion.apache.org/),
for Linux and Windows. Free and open source (GPL v3).

TortoiseSVN only runs on Windows and looks its age, and the cross-platform
clients are paid or unmaintained. TrunkLine aims to be a clean, fast client
that works the same on both, built with Go and [Wails](https://wails.io/).

## What it does

- **Working copies**: a sidebar of your checkouts. Add one you already have, or
  check out from a repository.
- **Changes**: what `svn status` shows, with a diff of the picked file. Tick
  files and commit (Ctrl+Enter). New files and folders are added for you, and
  files deleted from disk are deleted in the repository. You can also revert,
  add and resolve conflicts (keep the file as edited, keep mine or take theirs).
- **Update**, with svn's output shown as it runs. **Check server** shows what an
  update would bring in without changing anything (`svn status -u`).
- **History**: the log of the working copy's URL. Click a revision to see its
  message and files, and a file to see what that revision changed in it.
- **Repositories**: save a repository URL, browse its folders and check out any
  folder (`trunk`, a branch, a tag).
- **Export** (repository browser): just the files of a folder, as a plain copy
  with no link to the repository (`svn export`).
- **Import folder here** (repository browser): commit a folder from your
  computer into a new repository folder (`svn import`). It refuses a folder
  that already has files. By default your folder then becomes the working copy
  (a checkout over it, like TortoiseSVN's "import in place"), so you keep
  working where you were. To take a friend's project into your own repo,
  export it from theirs, then import that folder into yours.
- **Logins**: when a server asks for one, a login dialog opens and the
  operation is retried. The password is kept for the session only and handed
  to svn on stdin. svn caches it too if a secure store is set up (GNOME
  Keyring or KWallet on Linux; the Windows credential store). Servers with a
  company or self-signed certificate ask once whether to trust them.

## Requirements

TrunkLine uses the real `svn` command-line client, so it behaves exactly like
the command line and works with the same working copies.

- **Linux**: `sudo apt install subversion`
- **Windows**: install [TortoiseSVN](https://tortoisesvn.net/) and tick
  "command line client tools" in its installer (or install SlikSVN). TrunkLine
  finds svn.exe on PATH or in their install folders.

## Building

Needs Go, plus `libgtk-3-dev` and `libwebkit2gtk-4.1-dev` on Linux.

```sh
make            # tests, then build/bin/trunkline
make run        # build and start it
make windows    # build/bin/trunkline.exe (cross-built, no cgo)
make test       # go test ./... (needs svn and svnadmin: the tests use a real repository)
make vet        # Linux and Windows
make dev        # debug build: Inspect in the window, and served on http://localhost:34115
make install-desktop   # a "TrunkLine (dev)" launcher in the app menu
```

The frontend has no build step: edit `frontend/dist` and rebuild.

## Layout

| Path | What |
|---|---|
| `main.go` | Wails start-up |
| `app.go` | the methods the window calls (`window.go.main.App.*`) |
| `internal/svn` | runs `svn` and parses its `--xml` output |
| `internal/config` | saved settings: `~/.config/trunkline/config.json` (`%AppData%\trunkline` on Windows) |
| `frontend/dist` | `index.html`, `app.js`, and `theme.css` |
| `packaging` | the icon |

## Status

Early days: the everyday workflow (checkout, update, commit, history, browse,
export, import) works and is tested against real repositories. Not yet there:
blame, switching branches, merging, side-by-side diffs. Bug reports and pull
requests are welcome.

## License

TrunkLine is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License, version 3 or (at your option) any
later version. See [LICENSE](LICENSE).
