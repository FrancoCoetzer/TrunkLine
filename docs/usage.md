# Using TrunkLine

TrunkLine is built around one idea: **the everyday work should take no
thought, and everything else should be there when you need it.** You can
commit and update all day without opening a menu. The deeper tools (merging,
locks, patches, blame, properties) stay out of the way until a file or a
task calls for them.

This guide follows the same order: the everyday loop first, then the rest,
one section per task. Press **F1** in the app for every keyboard shortcut.

## Contents

- [Getting started](#getting-started)
- [The everyday loop](#the-everyday-loop)
- [When you need more](#when-you-need-more)
  - [Conflicts](#conflicts)
  - [History, blame and comparing](#history-blame-and-comparing)
  - [Branches, tags and merging](#branches-tags-and-merging)
  - [Locking files you can't merge](#locking-files-you-cant-merge)
  - [Organising your changes](#organising-your-changes)
  - [Patches](#patches)
  - [Properties and externals](#properties-and-externals)
  - [Working with the repository directly](#working-with-the-repository-directly)
  - [Big repositories](#big-repositories)
- [From your file manager](#from-your-file-manager)
- [From a terminal file manager](#from-a-terminal-file-manager)
- [From the terminal](#from-the-terminal)
- [Keyboard](#keyboard)
- [Coming from TortoiseSVN](#coming-from-tortoisesvn)
- [When something goes wrong](#when-something-goes-wrong)

## Getting started

The first screen offers two ways in:

- **Add an existing working copy**: pick a folder you already checked out
  (with TortoiseSVN, the command line, anything). Nothing is converted or
  moved: TrunkLine runs the same `svn` you would.
- **Add a repository**: give a URL. You can then browse it and check out
  any folder (`trunk`, a branch, a tag) into a folder of your choice.

A working copy can also come from your file manager: right-click its folder
and pick **Open With → TrunkLine** (Linux), or drag the folder onto
TrunkLine's window. Either one adds it to the sidebar if it's new and opens
it. A file works too: it opens the working copy the file is in. A folder
that isn't a working copy yet gets two offers: **Check out into it** (bring
a repository folder's files into it; files already there are kept) or
**Import it** (commit its files into a new folder in a repository, and keep
working in it).

Both end up in the sidebar: **Working copies** (your checkouts) and
**Repositories** (saved URLs to browse). Click one to open it.

If the server asks for a login, a login dialog opens and TrunkLine retries.
A server with a company or self-signed certificate asks once whether to
trust it.

## The everyday loop

Most days you only need the **Changes** tab and two buttons.

1. **Update** (top right) brings in what others committed. The button shows
   how many files changed on the server, checked in the background, so you
   know when it's worth pressing.
2. **Edit** your files in your usual editor. TrunkLine notices.
3. **Changes** lists what you changed. Click a file (or move with ↑ ↓) to
   see its diff on the right.
4. **Tick** what to commit (Space ticks the selected file), type a message
   and press **Commit** or **Ctrl+Enter**.

Things TrunkLine does for you so you don't have to think about them:

- New files you tick are added, and files you deleted from disk are deleted
  in the repository, as part of the commit.
- A file you renamed in your file manager shows up as a missing file plus a
  new one; **Record as renames** keeps its history.
- Files you never want to commit (build output, logs): select one and press
  **Ignore…** to ignore it by name or by extension, here or in subfolders.
- A file that still has conflict markers (`<<<<<<<`) can't be committed or
  marked resolved by accident.

Undo before committing: tick files and press **Revert**.

## When you need more

Everything below is a click away from where you already are: a button above
the diff, a tab, or the **⋯** menu next to Update.

### Conflicts

An update, switch or merge that conflicts is pointed out, and the files are
marked in Changes.

- **Merge…** (above the diff of a conflicted file) opens the merge editor:
  for each conflict pick **Mine**, **Theirs**, **Mine, then theirs**,
  **Theirs, then mine**, or **Edit…** it by hand. **Next conflict** jumps
  ahead, **Show the original** adds the common ancestor, and **Save and mark
  resolved** finishes.
- For whole files: tick them, choose how in the box next to **Resolve**
  (keep mine, take theirs, or mark your edited file resolved), then press
  **Resolve**.

### History, blame and comparing

- **History** (Ctrl+2) is the log of the working copy. Search it by message,
  author or changed path; **Load more** goes further back.
- Click a revision to see its message and files; click a file to see what
  that revision changed in it.
- On a revision: **Undo this commit** takes its changes back out as local
  changes for you to check and commit; **Tag this revision…** marks it as a
  release.
- **History** and **Blame** above a diff do the same for one file. Blame
  shows who last changed each line and when, newest changes highlighted;
  click a revision for its message.
- **Compare** a revision, branch or tag with another ("what changed since
  v1.0?"); **⇄ Swap** turns it round.
- **Side by side** (above a diff, or the View menu for everywhere) shows old
  and new next to each other.
- **Revision graph** (View menu, or **Graph** on Branches) draws trunk,
  branches, merges and tags over time.

### Branches, tags and merging

The **Branches** tab (Ctrl+3) lists trunk, branches and tags with their last
change. The header shows which one you're on.

- **Switch** to another branch: your local changes come along.
- **New branch…** / **New tag…** from the latest revision or from your
  working copy's.
- **Merge into mine…** (on a branch's row) lists the commits of that branch
  you don't have yet. Take
  all of them (keep a branch up to date, or bring a finished one back) or
  tick just some (cherry-pick). svn remembers what was merged, so nothing is
  merged twice. The result lands as local changes with the message filled
  in: check, then commit, or **Revert** to back out.
- Repository without the trunk/branches/tags layout? **Switch to URL…**.
  Server address changed (http to https, a new name)? **Server moved…**.

### Locking files you can't merge

CAD models, Office documents, images: two people editing the same one can't
be merged afterwards. Lock it first.

- The **Locks** tab (Ctrl+4) lists files that need a lock (read-only until
  you lock them) and who holds which lock, with their note.
- **Lock** / **Unlock**, or **Lock a file…** for one not in the list.
- Committing releases your locks, unless you tick **Keep my locks**.
- Taking over someone else's lock asks first, and tells you whose it is.

### Organising your changes

- **Changelist…**: group changed files under a name ("login fix") to commit
  them apart from your other work. Each group gets a heading in Changes whose
  box ticks the whole group.
- **Show unversioned files** (View menu, or the box above the list) hides or
  shows files svn doesn't know about.
- **Rename** (above the diff) or **Rename or move a file or folder…** (⋯)
  moves it with its history.

### Patches

From the **⋯** menu:

- **Save ticked changes as a patch…**: send someone your changes without
  committing. New, deleted and binary files and property changes are included.
- **Apply a patch…**: checks first and says which files it would change and
  which parts won't fit. Patches work with `svn patch` and TortoiseSVN too.

### Properties and externals

**Properties** (above a diff, or **Properties of the top folder** in ⋯) shows
a file's or folder's svn properties with a short explanation of each one svn
knows: line endings, needs-lock, ignore lists, and your own. `svn:externals`
(folders fetched from elsewhere) is edited as a table of URL, revision and
folder. Like any change, properties are shared by committing them.

### Working with the repository directly

Open a saved repository from the sidebar to browse it. From there, without a
working copy:

- **Check out this folder**, **Export** (a plain copy, no link to svn), or
  **Import folder here…** (commit a folder from your computer; by default it
  then becomes your working copy).
- **New folder…**, **Rename or move…**, **Delete…** and **Copy URL**, each
  as one commit.
- Click a file to see its blame.

### Big repositories

- Check out only the top folder, then **Choose folders…** (⋯) to tick the
  folders you want and untick the ones you don't. They stay in the
  repository; you just don't download them.
- **Check the server for changes** (⋯) shows what an update would bring in
  without changing anything.
- **Clean up** (⋯) if an operation was interrupted and svn says the working
  copy is locked.

## From your file manager

On Linux, TrunkLine can add a **TrunkLine** submenu to the file manager's
right-click menu: Commit, Update, Show log, Diff, Blame, Add, Revert, Lock
and Release lock, each opening TrunkLine at that task (as the commands
below do). Outside a working copy, a folder gets **Check out or import…**.
Set it up once, for your user:

```sh
trunkline integrate          # adds the menu to the file managers it finds
trunkline integrate --remove # takes it out again
```

Supported so far:

- **Nautilus** (GNOME Files), which needs its Python support (`sudo apt
  install python3-nautilus`, Fedora: `nautilus-python`). Restart it
  afterwards with `nautilus -q`.
- **Nemo** (Cinnamon, Linux Mint) and **Caja** (MATE), the same way: they
  need `nemo-python` or `python3-caja` (Fedora: `nemo-python`,
  `caja-python`); restart with `nemo -q` or `caja -q`.
- **Thunar** (XFCE). TrunkLine adds its items to Thunar's custom actions
  (`~/.config/Thunar/uca.xml`), next to your own, which it leaves as they
  are; `--remove` takes only TrunkLine's out. Like Dolphin, it shows the
  same items everywhere. Restart Thunar with `thunar -q`.
- **PCManFM-Qt** (LXQt) and **PCManFM** (LXDE): a TrunkLine submenu from
  file-manager actions in `~/.local/share/file-manager/actions`, the same
  items everywhere. Close PCManFM-Qt's windows and open it again.
- **elementary Files**: Commit, Update, Show log and Open in TrunkLine, as
  Contractor items (`~/.local/share/contractor`). Files shows these
  directly in its menu rather than in a submenu, so only the everyday four.
- **Dolphin** and Konqueror (KDE). Dolphin can't tell whether a file is in a
  working copy, so the menu shows the same items everywhere; outside a
  working copy they offer a checkout or an import. For svn status icons on
  files, install KDE's `dolphin-plugins` and turn on its Subversion plugin
  in Dolphin's settings.

Run `trunkline integrate` again after moving TrunkLine: the menu runs the
program from where it was then.

**Help wanted:** the menus for Nautilus, Dolphin and Thunar have been tried
by hand. Those for Nemo, Caja, PCManFM-Qt, PCManFM and elementary Files come
from the same list and are checked by tests, but nobody has tried them in
those file managers yet. If you use one, please tell us how it went, whether
it works or not: [open an issue](https://github.com/FrancoCoetzer/TrunkLine/issues).

Every file manager also has **Open With → TrunkLine** on folders, with no
setup, and you can drag a folder onto TrunkLine's window.

## From a terminal file manager

ranger, lf, nnn and yazi have no menus to add to, but a few lines in their
config give the same actions on keys. Each snippet uses the same keys:
**T** (in nnn: **;**) then **c** commit, **l** show log, **d** diff, **b**
blame, on the selected files or the highlighted one; **u** update and **o**
open, on the folder you're in. Flatpak and snap users: put `flatpak run
io.github.FrancoCoetzer.TrunkLine` or `snap run trunkline` where it says
`trunkline`.

**ranger** (`~/.config/ranger/rc.conf`):

```
map Tc shell -f trunkline commit -- %s
map Tl shell -f trunkline log -- %s
map Td shell -f trunkline diff -- %s
map Tb shell -f trunkline blame -- %s
map Tu shell -f trunkline update -- %d
map To shell -f trunkline open -- %d
```

**lf** (`~/.config/lf/lfrc`):

```
cmd trunkline-on-files &{{
    set -f
    IFS='
'
    trunkline "$1" -- $fx
}}
map Tc trunkline-on-files commit
map Tl trunkline-on-files log
map Td trunkline-on-files diff
map Tb trunkline-on-files blame
map Tu &trunkline update -- "$PWD"
map To &trunkline open -- "$PWD"
```

**nnn** (in your shell's startup file, e.g. `~/.bashrc`; add these to any
`NNN_PLUG` you already have, separated by `;`, and change a key if it's
taken):

```sh
export NNN_PLUG='c:-!&trunkline commit -- "$nnn";l:-!&trunkline log -- "$nnn";d:-!&trunkline diff -- "$nnn";b:-!&trunkline blame -- "$nnn";u:-!&trunkline update -- "$PWD";o:-!&trunkline open -- "$PWD"'
```

**yazi** (`~/.config/yazi/keymap.toml`; versions before 25.5 call the
section `manager` instead of `mgr`):

```toml
[[mgr.prepend_keymap]]
on   = ["T", "c"]
run  = 'shell --orphan -- trunkline commit -- "$@"'
desc = "TrunkLine: commit"

[[mgr.prepend_keymap]]
on   = ["T", "l"]
run  = 'shell --orphan -- trunkline log -- "$@"'
desc = "TrunkLine: log"

[[mgr.prepend_keymap]]
on   = ["T", "d"]
run  = 'shell --orphan -- trunkline diff -- "$@"'
desc = "TrunkLine: diff"

[[mgr.prepend_keymap]]
on   = ["T", "b"]
run  = 'shell --orphan -- trunkline blame -- "$@"'
desc = "TrunkLine: blame"

[[mgr.prepend_keymap]]
on   = ["T", "u"]
run  = 'shell --orphan -- trunkline update -- "$PWD"'
desc = "TrunkLine: update"

[[mgr.prepend_keymap]]
on   = ["T", "o"]
run  = 'shell --orphan -- trunkline open -- "$PWD"'
desc = "TrunkLine: open"
```

The ranger, lf and nnn snippets were tried in ranger 1.9.4, lf 38 and nnn 5.1;
the yazi one hasn't been tried yet. If it works for you, or doesn't, please
[open an issue](https://github.com/FrancoCoetzer/TrunkLine/issues).

## From the terminal

`trunkline <action> <paths>` opens TrunkLine at that task, in the working
copy that holds the paths (added to the sidebar if it's new). Paths default
to the current folder. If TrunkLine is already open, its window takes the
task: there's only ever one window.

| Command | What happens |
|---|---|
| `trunkline` or `trunkline <path>` | Opens the working copy |
| `trunkline commit [paths]` | Changes, with those files ticked and the message box ready |
| `trunkline update` | Updates the working copy |
| `trunkline log [path]` | History of the working copy, or of one file or folder |
| `trunkline diff <file>` | The diff of a changed file |
| `trunkline blame <file>` | Blame for the file |
| `trunkline lock <files>` / `unlock <files>` | Lock (asks for a note) / release your locks |
| `trunkline revert [paths]` | Revert, after asking first |
| `trunkline add [paths]` | Adds new files |

Nothing happens without the window showing it: revert still asks, and commit
waits for your message. `trunkline --help` lists the actions.

Where `trunkline` is: on PATH with the .deb and .rpm; `flatpak run
io.github.FrancoCoetzer.TrunkLine commit .` for the Flatpak;
`snap run trunkline commit .` for the snap; on Windows, `trunkline.exe` in
the folder you installed it to.

## Keyboard

Press **F1** for the full list. The ones worth learning:

| Keys | What |
|---|---|
| Ctrl+Enter | Commit (in the message box) |
| Ctrl+1 … Ctrl+4 | Changes, History, Branches, Locks |
| ↑ ↓ Home End | Move through a list (shows the diff or revision) |
| Space | Tick or untick |
| Enter | Open the file |
| F5 | Refresh |
| Alt+F / Alt+V / Alt+H | File, View, Help menus |
| Ctrl+, | Preferences |
| Ctrl+= / Ctrl+− / Ctrl+0 | Zoom |
| Esc | Close a dialog, menu or message |

## Coming from TortoiseSVN

Your working copies work as they are: add them with **Add an existing
working copy**. Where things are:

| TortoiseSVN | TrunkLine |
|---|---|
| SVN Commit… | Changes tab: tick, message, Ctrl+Enter |
| SVN Update | Update button |
| Check for modifications | Changes tab (always up to date); ⋯ → Check the server for changes |
| Show log | History tab |
| Blame… | Blame above the diff |
| Diff / Diff with previous version | Click the file in Changes or History |
| Revision graph | View → Revision graph |
| Branch/tag… | Branches → New branch… / New tag… |
| Switch… | Branches → Switch |
| Merge… | Branches → Merge into mine… |
| Edit conflicts / Resolved… | Merge… above the diff / Resolve |
| Get lock… / Release lock | Locks tab |
| Revert… | Revert |
| Add to ignore list | Ignore… |
| Create patch… / Apply patch… | ⋯ → Save ticked changes as a patch… / Apply a patch… |
| Properties | Properties above the diff |
| Relocate… | Branches → Server moved… |
| Repo-browser | Repositories in the sidebar |
| Clean up… | ⋯ → Clean up |

## When something goes wrong

- **"Working copy locked"** after a crash or a cancelled operation: ⋯ →
  **Clean up**.
- **Login keeps being asked for**: TrunkLine keeps the password for the
  session only. For svn to remember it, set up a keyring (GNOME Keyring or
  KWallet on Linux; Windows does this itself).
- **svn not found** (Windows): install TortoiseSVN with "command line client
  tools" ticked, or SlikSVN.
- Anything else: **Help → Report a problem…**.
