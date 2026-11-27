"use strict";
// TrunkLine's page: start; look; keyboard shortcuts; menu commands and their shortcuts; opened from outside.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Start ------------------------------------------------------------------------------------

// ---- Look: theme and font ---------------------------------------------------------------------
//
// The choice is saved in the settings file (SetLook) and copied to
// localStorage, which the script in index.html's <head> reads to apply it
// before anything paints.

const ZOOMS = [50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200];
const look = { theme: "system", font: "mono", zoom: 100 };
$("lookZoom").append(...ZOOMS.map(z => el("option", { value: z }, z + "%")));

function applyLook() {
  const root = document.documentElement;
  if (look.theme === "dark" || look.theme === "light") root.dataset.theme = look.theme; else delete root.dataset.theme;
  if (look.font === "regular") root.dataset.font = look.font; else delete root.dataset.font;
  root.style.zoom = look.zoom === 100 ? "" : String(look.zoom / 100);
  store("theme", look.theme);
  store("font", look.font);
  store("zoom", String(look.zoom));
  $("lookTheme").value = look.theme;
  $("lookFont").value = look.font;
  $("lookZoom").value = String(look.zoom);
  syncWindowColour();
}

// setLook changes some of theme, font and zoom, shows it, and saves it.
async function setLook(change) {
  Object.assign(look, change);
  applyLook();
  try { await call("SetLook", look.theme, look.font, look.zoom); } catch (e) { report(e); }
}

// stepZoom moves one step up (1) or down (-1) the zoom levels.
function stepZoom(dir) {
  const i = ZOOMS.findIndex(z => z >= look.zoom);
  const next = ZOOMS[Math.min(ZOOMS.length - 1, Math.max(0, (i < 0 ? ZOOMS.length - 1 : i) + dir))];
  if (next !== look.zoom) setLook({ zoom: next });
  toast(`Size ${next}%`);
}

// syncWindowColour gives the window behind the page (seen while it opens or
// resizes) the page's background.
function syncWindowColour() {
  const m = getComputedStyle(document.body).backgroundColor.match(/\d+/g);
  if (m && window.runtime && window.runtime.WindowSetBackgroundColour) window.runtime.WindowSetBackgroundColour(+m[0], +m[1], +m[2], 255);
}
matchMedia("(prefers-color-scheme: light)").addEventListener("change", syncWindowColour);

$("lookTheme").onchange = () => setLook({ theme: $("lookTheme").value });
$("lookFont").onchange = () => setLook({ font: $("lookFont").value });
$("lookZoom").onchange = () => setLook({ zoom: Number($("lookZoom").value) });

function openPrefs() {
  applyLook();
  $("prefsAuto").checked = state.startup.updateCheck;
  $("prefsNotify").checked = state.startup.notify;
  if (!$("prefsDlg").open) ask($("prefsDlg"));
}

// ---- Keyboard shortcuts ------------------------------------------------------------------------

const SHORTCUTS = [
  ["Anywhere"],
  ["Alt+F, Alt+V, Alt+H", "Open the File, View or Help menu (then ← → ↑ ↓)"],
  ["Ctrl+,", "Preferences"],
  ["F1", "This list"],
  ["F5", "Refresh"],
  ["Ctrl+= / Ctrl+− / Ctrl+0", "Bigger, smaller, actual size"],
  ["Esc", "Close a dialog, menu or message"],
  ["Ctrl+Q", "Quit"],
  ["In a working copy"],
  ["Ctrl+1 … Ctrl+4", "Changes, History, Branches, Locks"],
  ["Ctrl+Enter", "Commit (in the message box)"],
  ["In a list"],
  ["↑ ↓ Home End", "Move (shows the diff or the revision)"],
  ["Space", "Tick or untick"],
  ["Enter", "Open the file"],
];
function showKeys() {
  $("keysList").replaceChildren(...SHORTCUTS.map(([k, what]) => what
    ? el("tr", {}, el("td", {}, k), el("td", {}, what))
    : el("tr", { class: "head" }, el("td", { colspan: 2 }, k))));
  if (!$("keysDlg").open) ask($("keysDlg"));
}

// ---- Menu commands and their shortcuts --------------------------------------------------------

async function quitApp() {
  if ((inFlight > 0 || flowRunning) && !await confirmDanger("Quit while svn is working?",
    "An svn command is still running. Quitting now stops it, which can leave the working copy needing a clean up.", "Quit")) return;
  window.runtime.Quit();
}

async function openRepoPage(path) {
  try { await Go().OpenReleasePage(await Go().RepoURL() + path); } catch (e) { report(e); }
}

const commands = {
  addWc: () => $("addWcBtn").onclick(),
  addServer: () => $("addServerBtn").onclick(),
  prefs: openPrefs,
  quit: quitApp,
  tab: b => { if (state.view && state.view.type === "wc") setTab(b.dataset.tab); },
  unversioned: () => $("showUnversioned").click(),
  sbs: () => setSideBySide(!sideBySide()),
  refresh: refreshView,
  theme: b => setLook({ theme: b.dataset.theme }),
  zoomIn: () => stepZoom(1),
  zoomOut: () => stepZoom(-1),
  zoomReset: () => { setLook({ zoom: 100 }); toast("Size 100%"); },
  keys: showKeys,
  checkUpdate: async () => { await openAbout(); $("aboutCheck").click(); },
  releases: () => openRepoPage("/releases"),
  issue: () => openRepoPage("/issues/new"),
  github: () => openRepoPage(""),
  about: openAbout,
  graph: () => flow(openGraph),
  entryMove: () => flow(entryMove),
  entryDelete: () => flow(entryDelete),
  entryCopyUrl: () => copyText(state.entry.url),
};

document.addEventListener("keydown", e => {
  const ctrl = (e.ctrlKey || e.metaKey) && !e.altKey;
  let cmd = null;
  if (e.key === "F1" && !modalOpen()) cmd = "keys";
  else if (e.key === "F5" && !modalOpen()) cmd = "refresh";
  else if (ctrl && (e.key === "=" || e.key === "+")) cmd = "zoomIn";
  else if (ctrl && (e.key === "-" || e.key === "_")) cmd = "zoomOut";
  else if (ctrl && e.key === "0") cmd = "zoomReset";
  else if (ctrl && !e.shiftKey && e.key === "," && !modalOpen()) cmd = "prefs";
  else if (ctrl && !e.shiftKey && e.key.toLowerCase() === "q") cmd = "quit";
  if (!cmd) return;
  e.preventDefault();
  commands[cmd]();
});

async function start() {
  const s = await Go().Startup();
  state.startup = s;
  Object.assign(look, { theme: s.theme, font: s.font, zoom: s.zoom });
  applyLook();
  if (s.settingsNote) toast(s.settingsNote, "err");
  state.wcs = s.workingCopies;
  state.servers = s.servers;
  $("version").textContent = s.version;
  if (s.svnError) {
    $("svnChip").hidden = false;
    const win = s.os === "windows";
    $("noSvn").textContent = "The svn command-line client was not found: " + s.svnError + ". " + (win
      ? "Install TortoiseSVN and tick \"command line client tools\" in its installer (or install SlikSVN), then restart TrunkLine."
      : "Install it (Ubuntu: sudo apt install subversion), then restart TrunkLine.");
    $("noSvn").hidden = false;
  } else {
    $("svnChip").hidden = true;
  }
  $("showUnversioned").checked = store("showUnversioned") !== "0";
  renderSidebar();

  // Started as `trunkline commit file.c` (or from a file manager's menu)?
  const pending = await Go().PendingAction().catch(() => null);
  if (pending && !pending.error) return runAction(pending);
  if (pending) toast(pending.error, "err");

  let last = null;
  try { last = JSON.parse(store("last") || "null"); } catch { /* ignore */ }
  if (last && last.type === "wc" && state.wcs.some(w => w.path === last.path)) openWc(last.path);
  else if (last && last.type === "browse" && state.servers.some(x => x.url === last.server)) openBrowse(last.server, last.server, last.name);
  else showView("welcome");
}

// ---- Opened from outside: the command line and file managers -----------------------------------
//
// `trunkline commit file.c`, or a file manager's menu running the same, arrives
// here: at start-up through PendingAction, or from a second launch as the
// "action" event (Go brings the window forward). Go has already found the
// working copy and added it to the list; act.paths are relative to it, ""
// being the working copy itself. Each action ends where the user would have
// got to by hand, and asks first wherever the buttons ask.

async function runAction(act) {
  if (!act) return;
  if (act.error) return toast(act.error, "err");
  if (modalOpen() || flowRunning) return toast("Finish what's open first, then try again.", "err");
  await reloadLists();
  await openWc(act.wc);
  if (act.skipped && act.skipped.length) {
    toast(`Opened ${shortPath(act.wc)}. Skipped ${act.skipped.length === 1 ? act.skipped[0] : act.skipped.length + " paths"}: one working copy at a time.`);
  }
  const paths = act.paths && act.paths.length ? act.paths : [""];
  const files = paths.filter(p => p !== "");
  const first = paths[0];
  const named = paths.length === 1 ? (first || "the working copy") : paths.length + " paths";
  // under: a change at or below one of the paths (or the new folder a path is in).
  const under = c => paths.some(p => p === "" || c.path === p || c.path.startsWith(p + "/") ||
    (c.isDir && c.item === "unversioned" && p.startsWith(c.path + "/")));
  const tick = (want, none) => {
    setTab("changes");
    const pick = state.changes.filter(c => under(c) && localChange(c) && want(c));
    if (!pick.length) { toast(none); return null; }
    if (pick.some(c => c.item === "unversioned")) $("showUnversioned").checked = true;
    state.checked = new Set(pick.map(c => c.path));
    renderChanges();
    showDiff(pick[0]);
    return pick;
  };

  switch (act.action) {
  case "open":
    return;
  case "commit":
    if (tick(c => c.item !== "unversioned", `Nothing to commit in ${named}.`)) $("commitMsg").focus();
    return;
  case "revert":
    if (tick(c => c.item !== "unversioned", `Nothing to revert in ${named}.`)) $("revertBtn").click();
    return;
  case "add":
    if (tick(c => c.item === "unversioned", `Nothing new to add in ${named}.`)) $("addBtn").click();
    return;
  case "update":
    $("updateBtn").click();
    return;
  case "log":
    if (first) showFileHistory(first);
    else setTab("history");
    return;
  case "diff": {
    setTab("changes");
    const c = state.changes.find(c => c.path === first && localChange(c));
    if (c) showDiff(c);
    else toast(`${first || "The working copy"} has no local changes to show.`);
    return;
  }
  case "blame":
    if (!files.length) return toast("Blame shows the lines of a file: pick a file, not the folder.");
    return openBlame(() => call("BlameFile", act.wc, files[0]), "", files[0]);
  case "lock":
    if (!files.length) return toast("Locks are on files: pick the files to lock.");
    return askLockAndLock(files);
  case "unlock":
    if (!files.length) return toast("Locks are on files: pick the files to unlock.");
    try {
      await call("UnlockFiles", act.wc, files, false);
      toast(files.length === 1 ? `Released the lock on ${files[0]}.` : `Released ${files.length} locks.`, "ok");
    } catch (e) { report(e); }
    return refreshWc(false);
  default:
    toast(`TrunkLine can't "${act.action}" yet.`, "err");
  }
}
window.runtime.EventsOn("action", act => runAction(act));

// ---- Dropped on the window ----------------------------------------------------------------------
//
// A folder (or a file) dragged from the file manager onto the window opens its
// working copy, as `trunkline <path>` does. Wails hands over the paths
// (DragAndDrop.EnableFileDrop); the hint shows while something is dragged over.

async function openDropped(paths) {
  hideDropHint();
  if (!paths || !paths.length) return;
  try { await runAction(await call("OpenDropped", paths)); } catch (e) { report(e); }
}

let dropHintTimer = 0;
function hideDropHint() { clearTimeout(dropHintTimer); $("dropHint").hidden = true; }
// Files from the file manager: WebView2 calls them "Files", WebKitGTK only
// "text/uri-list".
const draggingFiles = e => !!e.dataTransfer && [...e.dataTransfer.types].some(t => t === "Files" || t === "text/uri-list");
// Any drop from outside is ours: the webview's default would open the folder,
// file or link as a page in place of TrunkLine (the page has no drags of its own).
document.addEventListener("dragover", e => {
  e.preventDefault();
  if (!draggingFiles(e)) return;
  $("dropHint").hidden = false;
  // dragover repeats while the pointer is over the window; when it stops, the drag has left
  clearTimeout(dropHintTimer);
  dropHintTimer = setTimeout(hideDropHint, 250);
});
document.addEventListener("drop", e => { e.preventDefault(); hideDropHint(); });
if (window.runtime.OnFileDrop) window.runtime.OnFileDrop((_x, _y, paths) => openDropped(paths), false);

start();
