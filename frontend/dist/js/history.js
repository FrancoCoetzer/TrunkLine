"use strict";
// TrunkLine's page: working copy buttons; incoming changes; tabs and history.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Working copy buttons ----------------------------------------------------------------

// ---- Incoming changes: "Update (3)" ----------------------------------------------------------

const INCOMING_EVERY = 5 * 60 * 1000;
let lastIncomingCheck = 0;

let incomingShown = 0;
function setIncoming(n) {
  const b = $("updateBtn");
  const what = `${n} file${n === 1 ? "" : "s"} changed on the server since your last update`;
  // The badge is made anew only when the count goes up, so it pops in then and not on every check.
  if (!n) b.replaceChildren("Update");
  else if (n > incomingShown || !b.querySelector(".badge")) b.replaceChildren("Update", el("span", { class: "badge", "aria-hidden": "true" }, String(n)));
  else b.querySelector(".badge").textContent = String(n);
  incomingShown = n;
  b.title = n ? what + ": svn update" : "svn update";
  b.setAttribute("aria-label", n ? `Update (${what})` : "Update");
  b.classList.toggle("has-incoming", n > 0);
}

// checkIncoming asks the server, in the background, what an update would
// bring for the open working copy. It never shows a login or an error (a
// background check mustn't interrupt): on failure the badge stays as it was.
async function checkIncoming() {
  if (!state.view || state.view.type !== "wc") return;
  // Hidden (minimised): only to notify, if that's on.
  if (document.hidden && !(state.startup && state.startup.notify)) return;
  const wc = state.view.path;
  lastIncomingCheck = Date.now();
  try {
    const n = await Go().Incoming(wc);
    if (state.view && state.view.path === wc) setIncoming(n);
    maybeNotify(wc, n);
  } catch { /* offline, or needs a login: try again later */ }
}

// maybeNotify shows a desktop notification when more incoming changes
// appear while TrunkLine isn't in front. The first count seen for a working
// copy is only noted (it was there before), and an update starts afresh.
const notifiedCount = {};
function maybeNotify(wc, n) {
  const seen = notifiedCount[wc];
  notifiedCount[wc] = Math.max(n, n === 0 ? 0 : seen || 0);
  if (seen === undefined || n <= seen || document.hasFocus() || !state.startup.notify) return;
  const name = (state.wcs.find(w => w.path === wc) || {}).name || wc.split(/[\\/]/).pop();
  const files = n === 1 ? "1 file has" : n + " files have";
  Go().Notify(`New commits in ${name}`, `${files} changed on the server. Update in TrunkLine to get them.`).catch(() => {});
}
setInterval(checkIncoming, INCOMING_EVERY);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && Date.now() - lastIncomingCheck > 60 * 1000) checkIncoming();
});

// conflictsAfter tells the user plainly when an update or switch left
// conflicts (svn finishes "successfully" either way), and shows them.
function conflictsAfter(what) {
  const n = state.changes.filter(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict).length;
  if (!n) return false;
  $("progDlg").close();
  setTab("changes");
  setBanner("conflict", `${what}, but ${n} file${n === 1 ? "" : "s"} ${n === 1 ? "has a conflict" : "have conflicts"} between your ` +
    "changes and the incoming ones (marked C). Pick each one, look at its diff, then resolve it below before committing.");
  return true;
}

$("updateBtn").onclick = () => busy($("updateBtn"), async () => {
  const wc = state.view.path;
  try {
    await withProgress("Updating " + $("wcName").textContent, () => call("Update", wc));
  } finally {
    state.log = [];
    await refreshWc(state.incoming);
    conflictsAfter("Updated");
    checkIncoming();
  }
});
$("checkBtn").onclick = () => busy($("checkBtn"), () => refreshWc(true));
$("refreshBtn").onclick = () => busy($("refreshBtn"), () => refreshWc(state.incoming));
$("openWcBtn").onclick = () => Go().Open(state.view.path, "");
$("cleanupBtn").onclick = () => busy($("cleanupBtn"), async () => {
  await call("Cleanup", state.view.path);
  toast("Cleanup done.", "ok");
  await refreshWc(false);
});
$("removeWcBtn").onclick = async () => {
  const wc = state.wcs.find(w => w.path === state.view.path);
  if (!await confirmDanger("Remove from the list?", `${wc ? wc.name : ""} (${state.view.path}) leaves the sidebar. The files stay on disk.`, "Remove")) return;
  await Go().RemoveWorkingCopy(state.view.path);
  state.view = null;
  store("last", "");
  await reloadLists();
  showView("welcome");
};
$("browseWcBtn").onclick = () => {
  if (state.info) openBrowse(state.info.repoRoot, state.info.url, null);
};

function refreshView() {
  if (state.view && state.view.type === "wc") refreshWc(state.incoming);
  else if (state.view && state.view.type === "browse") listBrowse();
}

// ---- Tabs and history ------------------------------------------------------------------------

const TABS = ["changes", "history", "branches", "locks"];
function currentTab() { const t = store("tab"); return TABS.includes(t) ? t : "changes"; }

function setTab(tab) {
  store("tab", tab);
  for (const b of document.querySelectorAll(".tabs [role=tab]")) {
    const on = b.dataset.tab === tab;
    b.setAttribute("aria-selected", on ? "true" : "false");
    b.tabIndex = on ? 0 : -1; // one Tab stop for the tab row; arrow keys move within it
  }
  for (const t of TABS) $("tab-" + t).hidden = tab !== t;
  if (tab === "history" && !state.log.length && state.info) loadLog(true);
  if (tab === "locks" && state.view && state.view.type === "wc") loadLocks();
  if (tab === "branches" && state.view && state.view.type === "wc") loadBranches();
}
for (const b of document.querySelectorAll(".tabs [role=tab]")) b.onclick = () => setTab(b.dataset.tab);

// Arrow keys, Home and End move between the tabs; Enter or Space (a click)
// opens one. Opening asks the server for History, Branches and Locks, so
// focus moving past a tab doesn't open it.
document.querySelector(".tabs").addEventListener("keydown", e => {
  const tabs = [...document.querySelectorAll(".tabs [role=tab]")];
  const i = tabs.indexOf(document.activeElement);
  if (i < 0) return;
  const to = { ArrowRight: (i + 1) % tabs.length, ArrowLeft: (i - 1 + tabs.length) % tabs.length, Home: 0, End: tabs.length - 1 }[e.key];
  if (to === undefined) return;
  e.preventDefault();
  for (const t of tabs) t.tabIndex = t === tabs[to] ? 0 : -1;
  tabs[to].focus();
});
// Ctrl+1..4 open the tabs from anywhere in a working copy.
document.addEventListener("keydown", e => {
  if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || modalOpen() || !state.view || state.view.type !== "wc") return;
  const tab = TABS[Number(e.key) - 1];
  if (!tab) return;
  e.preventDefault();
  setTab(tab);
  document.getElementById("tabbtn-" + tab).focus();
});

const LOG_PAGE = 100;

// What the History tab shows: the working copy's history, or one file's
// (historyFile: {url, label}), optionally filtered by a search.
state.historyFile = null;
state.logSearch = "";
const SEARCH_LIMIT = 500;

function logTarget() { return state.historyFile ? state.historyFile.url : state.info.url; }

async function loadLog(fresh) {
  if (!state.info) return;
  const url = logTarget(), search = state.logSearch;
  const from = fresh || !state.log.length ? 0 : state.log[state.log.length - 1].revision - 1;
  if (!fresh && from < 1) return;
  showLogStatus(search ? "Searching all of the history…" : "");
  try {
    const entries = await call("Log", url, from, search ? SEARCH_LIMIT : LOG_PAGE, search);
    if (!state.info || logTarget() !== url || state.logSearch !== search) return;
    state.log = fresh ? entries : state.log.concat(entries);
    if (fresh) { state.logRev = null; $("revDetail").hidden = true; $("revDiff").replaceChildren(); }
    renderLog();
    $("moreLog").hidden = search ? true : entries.length < LOG_PAGE;
    showLogStatus(search ? (entries.length >= SEARCH_LIMIT ? `The newest ${SEARCH_LIMIT} matches for "${search}"`
      : `${entries.length} match${entries.length === 1 ? "" : "es"} for "${search}"`) : "");
  } catch (e) { showLogStatus(""); report(e); }
}
$("moreLog").onclick = () => loadLog(false);

function showLogStatus(text) {
  const filtered = !!(state.historyFile || state.logSearch);
  $("logFile").hidden = !state.historyFile;
  if (state.historyFile) $("logFile").textContent = "History of " + state.historyFile.label;
  $("logStatus").hidden = !text && !filtered;
  $("logStatusText").textContent = text;
  $("logShowAll").hidden = !filtered;
}

$("logSearch").addEventListener("keydown", e => {
  if (e.key !== "Enter") return;
  state.logSearch = $("logSearch").value.trim();
  state.log = [];
  loadLog(true);
});
$("logSearch").addEventListener("search", () => { // the clear (x) button
  if ($("logSearch").value === "" && state.logSearch) { state.logSearch = ""; state.log = []; loadLog(true); }
});
$("logShowAll").onclick = () => {
  state.historyFile = null;
  state.logSearch = "";
  $("logSearch").value = "";
  state.log = [];
  loadLog(true);
};

// showFileHistory opens the History tab on one working copy file.
function showFileHistory(rel) {
  state.historyFile = { url: state.info.url.replace(/\/+$/, "") + "/" + encodePath(rel), label: rel };
  state.logSearch = "";
  $("logSearch").value = "";
  state.log = [];
  setTab("history");
  loadLog(true);
}

function renderLog() {
  const tb = $("log");
  const hadFocus = tb.contains(document.activeElement);
  tb.replaceChildren();
  for (const e of state.log) {
    const first = (e.message || "").split("\n")[0];
    const tr = el("tr", { class: e.revision === state.logRev ? "sel" : "", onclick: () => showRevision(e) },
      el("td", { class: "muted" }, "r" + e.revision),
      el("td", {}, e.author || ""),
      el("td", { class: "muted" }, fmtDate(e.date)),
      el("td", { class: "msg", title: e.message }, first));
    tr.dataset.key = String(e.revision);
    tr._move = tr._enter = () => showRevision(e);
    tb.append(tr);
  }
  if (!state.log.length) tb.append(el("tr", {}, el("td", { class: "placeholder" }, "No history")));
  rovingRows(tb, hadFocus);
}

function showRevision(e) {
  state.logRev = e.revision;
  renderLog();
  const root = state.info.repoRoot;
  const d = $("revDetail");
  d.hidden = false;
  const paths = el("div", { class: "paths" });
  const pick = (div, url, rev) => {
    for (const x of paths.children) x.classList.remove("sel");
    if (div) div.classList.add("sel");
    $("revDiff").replaceChildren(el("div", { class: "placeholder" }, "Loading…"));
    call("RevisionDiff", url, rev)
      .then(t => { if (state.logRev === rev) renderDiff($("revDiff"), t); })
      .catch(err => $("revDiff").replaceChildren(el("div", { class: "placeholder" }, parseErr(err).message)));
  };
  for (const p of e.paths) {
    // A deleted path only exists in the revision before.
    const peg = p.action === "D" ? e.revision - 1 : e.revision;
    const div = el("div", {},
      el("span", { class: "st st-" + ({ A: "added", D: "deleted", M: "modified", R: "replaced" }[p.action] || "") }, p.action),
      " " + p.path + (p.copyFrom ? `  (from ${p.copyFrom}@${p.copyRev})` : ""));
    if (p.kind !== "dir") div.onclick = () => pick(div, root + encodePath(p.path) + "@" + peg, e.revision);
    else div.classList.add("muted");
    paths.append(div);
  }
  d.replaceChildren(
    el("div", {}, el("b", {}, "r" + e.revision), "  ", e.author || "", el("span", { class: "muted" }, "  " + fmtDate(e.date))),
    el("pre", {}, e.message || "(no message)"),
    el("div", { class: "row" },
      el("button", { onclick: () => pick(null, root + "@" + e.revision, e.revision) }, "Show all changes"),
      " ",
      el("button", { title: "Compare this revision with another, the latest, or a branch or tag",
        onclick: () => flow(() => openCompare({ url: logTarget(), rev: e.revision, label: `r${e.revision} of ${repoLabel(logTarget())}` })) }, "Compare…"),
      " ",
      el("button", { title: "Make a tag (e.g. a release) of the code as it was at this revision",
        onclick: () => flow(() => tagAt(e.revision)) }, "Tag this revision…"),
      " ",
      el("button", { class: "danger", title: "Take this commit's changes back out of your working copy, to review and commit",
        onclick: () => flow(() => undoRevision(e)) }, "Undo this commit")),
    paths);
  $("revDiff").replaceChildren(el("div", { class: "placeholder" }, "Pick a file to see what this revision changed in it."));
}
