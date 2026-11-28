# TrunkLine's menu in Nautilus (GNOME Files): right-click > TrunkLine.
#
# Written by `trunkline integrate` from TrunkLine's list of actions; it is
# rewritten on the next run, so changes here don't last. `trunkline integrate
# --remove` takes it out. Each item only runs TrunkLine's command line
# (`trunkline <action> <paths>`); TrunkLine's window does the rest.
#
# Menus are built on every right-click, so this never runs svn: whether a path
# is in a working copy comes from looking for a .svn folder above it.
# Also loads in the older API (Nautilus < 43: methods take the window first).

import json
import os

import gi

for _version in ("4.1", "4.0", "3.0"):
    try:
        gi.require_version("Nautilus", _version)
        break
    except ValueError:
        pass
from gi.repository import GLib, GObject, Nautilus  # noqa: E402

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
            item = Nautilus.MenuItem(name="TrunkLine::" + a["action"] + ("" if wc else "-outside"),
                                     label=a["label"], icon=a["icon"])
            item.connect("activate", lambda _item, act=a["action"]: launch(act, paths))
            items.append(item)
    if not items:
        return []
    top = Nautilus.MenuItem(name="TrunkLine::menu", label="TrunkLine", icon="trunkline")
    sub = Nautilus.Menu()
    top.set_submenu(sub)
    for item in items:
        sub.append_item(item)
    return [top]


def local_path(f):
    loc = f.get_location()
    return loc.get_path() if loc is not None else None


class TrunkLineMenu(GObject.GObject, Nautilus.MenuProvider):
    def get_file_items(self, *args):
        files = args[-1]  # (files), or (window, files) before Nautilus 43
        paths = [local_path(f) for f in files]
        if not paths or None in paths:
            return []  # nothing picked, or a file not on this computer
        return menu(paths, ["folder" if f.is_directory() else "file" for f in files])

    def get_background_items(self, *args):
        path = local_path(args[-1])  # (folder), or (window, folder) before Nautilus 43
        return menu([path], ["background"]) if path else []
