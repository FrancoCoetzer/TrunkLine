# TrunkLine's menu in __FM__: right-click > TrunkLine.
# (One file for Nautilus, Nemo and Caja: they share the extension API.)
#
# Written by `trunkline integrate` from TrunkLine's list of actions; it is
# rewritten on the next run, so changes here don't last. `trunkline integrate
# --remove` takes it out. Each item only runs TrunkLine's command line
# (`trunkline <action> <paths>`); TrunkLine's window does the rest.
#
# Menus are built on every right-click, so this never runs svn: whether a path
# is in a working copy comes from looking for a .svn folder above it.
# Also loads in the older API (Nautilus < 43, Nemo, Caja: methods take the
# window first).

import importlib
import json
import os

import gi

FILE_MANAGER = "__FM__"
for _version in {"Nautilus": ("4.1", "4.0", "3.0"), "Nemo": ("3.0",), "Caja": ("2.0",)}[FILE_MANAGER]:
    try:
        gi.require_version(FILE_MANAGER, _version)
        break
    except ValueError:
        pass
from gi.repository import GLib, GObject  # noqa: E402

FM = importlib.import_module("gi.repository." + FILE_MANAGER)

COMMAND = json.loads(r'''__COMMAND__''')
ACTIONS = json.loads(r'''__ACTIONS__''')


def wc_root(path):
    """The working copy that holds path (the nearest folder with .svn), or None."""
    d = path if os.path.isdir(path) else os.path.dirname(path)
    while True:
        if os.path.isdir(os.path.join(d, ".svn")):
            return d
        up = os.path.dirname(d)
        if up == d:
            return None
        d = up


def launch(action, paths):
    # "--": a path that starts with "-" is still a path
    GLib.spawn_async(COMMAND + [action, "--"] + paths, flags=GLib.SpawnFlags.SEARCH_PATH)


def menu(paths, kinds):
    """The TrunkLine submenu for paths; kinds says what each one is (file, folder, background)."""
    inside = [wc_root(p) is not None for p in paths]
    if all(inside):
        wc = True
    elif not any(inside):
        wc = False
    else:
        return []  # some in a working copy, some not: nothing fits all of them
    items = []
    for a in ACTIONS:
        if a["wc"] == wc and all(k in a["on"] for k in kinds):
            item = FM.MenuItem(name="TrunkLine::" + a["action"] + ("" if wc else "-outside"),
                                     label=a["label"], icon=a["icon"])
            item.connect("activate", lambda _item, act=a["action"]: launch(act, paths))
            items.append(item)
    if not items:
        return []
    top = FM.MenuItem(name="TrunkLine::menu", label="TrunkLine", icon="trunkline")
    sub = FM.Menu()
    top.set_submenu(sub)
    for item in items:
        sub.append_item(item)
    return [top]


def local_path(f):
    loc = f.get_location()
    return loc.get_path() if loc is not None else None


# Nemo lists extensions by name in its plugin settings.
_named = (FM.NameAndDescProvider,) if hasattr(FM, "NameAndDescProvider") else ()


class TrunkLineMenu(GObject.GObject, FM.MenuProvider, *_named):
    def get_name_and_desc(self):
        return ["TrunkLine:::Commit, update, log and more from the right-click menu"]

    def get_file_items(self, *args):
        files = args[-1]  # (files), or (window, files): Nautilus < 43, Nemo, Caja
        paths = [local_path(f) for f in files]
        if not paths or None in paths:
            return []  # nothing picked, or a file not on this computer
        return menu(paths, ["folder" if f.is_directory() else "file" for f in files])

    def get_background_items(self, *args):
        path = local_path(args[-1])  # (folder), or (window, folder)
        return menu([path], ["background"]) if path else []
