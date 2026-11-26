"use strict";
// TrunkLine's page logic. Every svn call goes through call(), which turns the
// Go side's JSON errors into a login or certificate dialog and retries.

const $ = id => document.getElementById(id);
const Go = () => window.go.main.App;

const state = {
  startup: null,
  wcs: [],
  servers: [],
  view: null,          // {type: "wc", path} or {type: "browse", root, url, server}
  info: null,          // svn info of the open working copy
  changes: [],
  checked: new Set(),  // ticked change paths
  known: new Set(),    // paths seen before (new ones get the default tick)
  diffPath: null,
  log: [],
  logRev: null,
  incoming: false,
};

// ---- Small helpers ----------------------------------------------------------

function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== undefined && v !== null && v !== false) e.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids) if (k !== null && k !== undefined) e.append(k);
  return e;
}

function store(key, value) {
  try {
    if (value === undefined) return localStorage.getItem(key);
    localStorage.setItem(key, value);
  } catch { /* storage off: the app still works */ }
  return null;
}

let toastTimer;
function toast(msg, cls = "") {
  const t = $("toast");
  t.textContent = msg;
  t.className = cls;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), cls === "err" ? 12000 : 5000);
}
$("toast").onclick = () => ($("toast").hidden = true);

function fmtDate(s) {
  if (!s || s.startsWith("0001")) return "";
  const d = new Date(s);
  const pad = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fmtSize(n) {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i ? n.toFixed(1) : n) + " " + u[i];
}

function sep() { return state.startup && state.startup.os === "windows" ? "\\" : "/"; }

// encodePath makes a repository path safe to put in a URL.
function encodePath(p) { return p.split("/").map(encodeURIComponent).join("/"); }

// ---- Dialogs -------------------------------------------------------------------

// ask opens a dialog and resolves with "ok" or "cancel". It listens for the
// form's submit and the dialog's cancel (Esc) rather than the close event,
// which a webview that is not painting (minimised, hidden) can hold back.
function ask(dlg) {
  return new Promise(resolve => {
    const form = dlg.querySelector("form");
    const done = v => {
      form.removeEventListener("submit", onSubmit);
      dlg.removeEventListener("cancel", onCancel);
      if (dlg.open) dlg.close(v);
      resolve(v);
    };
    const onSubmit = e => { e.preventDefault(); done(e.submitter && e.submitter.value === "ok" ? "ok" : "cancel"); };
    const onCancel = e => { e.preventDefault(); done("cancel"); };
    form.addEventListener("submit", onSubmit);
    dlg.addEventListener("cancel", onCancel);
    dlg.returnValue = "";
    dlg.showModal();
  });
}

// Cancel buttons must not be the form's default (Enter would press them);
// they cancel like Esc does.
for (const b of document.querySelectorAll('dialog button[value="cancel"]')) {
  b.type = "button";
  b.addEventListener("click", () => {
    const dlg = b.closest("dialog");
    if (dlg.dispatchEvent(new Event("cancel", { cancelable: true }))) dlg.close("cancel");
  });
}

function confirmDanger(title, msg, okLabel) {
  $("confirmTitle").textContent = title;
  $("confirmMsg").textContent = msg;
  $("confirmOk").textContent = okLabel;
  return ask($("confirmDlg")).then(r => r === "ok");
}

async function askLogin(err) {
  $("loginHost").textContent = err.host;
  const wrong = /E170001|authorization failed/i.test(err.message) && !/E215004/.test(err.message);
  $("loginWhy").textContent = wrong ? "The server refused that username or password." : "This server needs a login.";
  $("loginUser").value = (await Go().LastUsername(err.host)) || "";
  $("loginPass").value = "";
  const r = ask($("loginDlg"));
  ($("loginUser").value ? $("loginPass") : $("loginUser")).focus();
  if (await r !== "ok") return false;
  await Go().Login(err.host, $("loginUser").value.trim(), $("loginPass").value);
  return true;
}

async function askTrust(err) {
  $("certHost").textContent = err.host;
  $("certMsg").textContent = err.message;
  if (await ask($("certDlg")) !== "ok") return false;
  await Go().TrustHost(err.host);
  return true;
}

function parseErr(e) {
  if (e && typeof e === "object" && e.kind) return e;
  const s = String(e && e.message !== undefined ? e.message : e);
  try {
    const j = JSON.parse(s);
    if (j && j.kind) return j;
  } catch { /* not ours */ }
  return { kind: "other", message: s };
}

// call runs a Go method; a login or certificate problem asks the user and
// tries again, anything else is thrown as {kind, message}.
async function call(name, ...args) {
  let logins = 0;
  for (;;) {
    try {
      return await Go()[name](...args);
    } catch (e) {
      const err = parseErr(e);
      if (err.kind === "auth" && err.host && logins++ < 5 && await askLogin(err)) continue;
      if (err.kind === "cert" && err.host && await askTrust(err)) continue;
      throw err;
    }
  }
}

// report shows a failed call, with a hint for the usual fixes.
function report(err) {
  err = parseErr(err);
  if (err.kind === "canceled") return toast("Canceled.");
  let msg = err.message;
  if (err.kind === "locked") msg += "\n\nTry Cleanup.";
  if (err.kind === "conflict" && /out of date/i.test(msg)) msg += "\n\nSomeone committed first: Update, then commit again.";
  if (err.kind === "auth" || err.kind === "cert") msg = "Not connected: " + msg;
  toast(msg, "err");
}

async function busy(btn, f) {
  const was = btn.disabled;
  btn.disabled = true;
  try { return await f(); } catch (e) { report(e); } finally { btn.disabled = was; }
}

// ---- Progress (checkout and update) -------------------------------------------------

let progLines = [];
let progFlush = null;
window.runtime.EventsOn("op:line", line => {
  progLines.push(line);
  if (!progFlush) progFlush = setTimeout(flushProgress, 50);
});

function flushProgress() {
  clearTimeout(progFlush);
  progFlush = null;
  if (!progLines.length) return;
  const log = $("progLog");
  log.textContent += progLines.join("\n") + "\n";
  progLines = [];
  if (log.textContent.length > 400000) log.textContent = "…\n" + log.textContent.slice(-300000);
  log.scrollTop = log.scrollHeight;
}

// withProgress shows the progress dialog while f runs.
async function withProgress(title, f) {
  $("progTitle").textContent = title;
  $("progLog").textContent = "";
  $("progClose").disabled = true;
  $("progCancel").disabled = false;
  const dlg = $("progDlg");
  dlg.showModal();
  try {
    const r = await f();
    flushProgress();
    $("progTitle").textContent = title + ": done";
    return r;
  } catch (e) {
    flushProgress();
    const err = parseErr(e);
    if (!$("progLog").textContent) {
      // refused before svn printed anything: the toast says why
      $("progClose").disabled = false;
      dlg.close();
      throw err;
    }
    $("progTitle").textContent = title + (err.kind === "canceled" ? ": canceled" : ": failed");
    if (err.kind !== "canceled") $("progLog").textContent += "\n" + err.message + "\n";
    throw err;
  } finally {
    $("progClose").disabled = false;
    $("progCancel").disabled = true;
  }
}
$("progCancel").onclick = () => Go().Cancel();
$("progDlg").addEventListener("cancel", e => { if ($("progClose").disabled) e.preventDefault(); });

// ---- Sidebar -------------------------------------------------------------------------

function renderSidebar() {
  const wcList = $("wcList");
  wcList.replaceChildren();
  if (!state.wcs.length) wcList.append(el("div", { class: "empty" }, "None yet"));
  for (const wc of state.wcs) {
    const active = state.view && state.view.type === "wc" && state.view.path === wc.path;
    wcList.append(el("button", { class: "navitem" + (active ? " active" : ""), title: wc.path, onclick: () => openWc(wc.path) },
      wc.name, el("span", { class: "sub" }, wc.path)));
  }
  const sl = $("serverList");
  sl.replaceChildren();
  if (!state.servers.length) sl.append(el("div", { class: "empty" }, "None yet"));
  for (const s of state.servers) {
    const active = state.view && state.view.type === "browse" && state.view.server === s.url;
    sl.append(el("button", { class: "navitem" + (active ? " active" : ""), title: s.url, onclick: () => openBrowse(s.url, s.url, s.name) },
      s.name, el("span", { class: "sub" }, s.url)));
  }
}

function showView(which) {
  $("welcome").hidden = which !== "welcome";
  $("wcView").hidden = which !== "wc";
  $("browseView").hidden = which !== "browse";
}

async function reloadLists() {
  const s = await Go().Startup();
  state.wcs = s.workingCopies;
  state.servers = s.servers;
  renderSidebar();
}

async function addWorkingCopy() {
  const dir = await Go().PickFolder("Pick a folder you checked out");
  if (!dir) return;
  try {
    const wc = await call("AddWorkingCopy", dir);
    await reloadLists();
    openWc(wc.path);
  } catch (e) { report(e); }
}

async function addServer() {
  $("serverUrl").value = "";
  $("serverName").value = "";
  const r = ask($("serverDlg"));
  $("serverUrl").focus();
  if (await r !== "ok") return;
  const url = $("serverUrl").value.trim();
  if (!url) return;
  try {
    await call("AddServer", $("serverName").value.trim(), url);
    await reloadLists();
    const s = state.servers.find(s => s.url === url.replace(/\/+$/, ""));
    if (s) openBrowse(s.url, s.url, s.name);
  } catch (e) { report(e); }
}

$("addWcBtn").onclick = addWorkingCopy;
$("welcomeWc").onclick = addWorkingCopy;
$("addServerBtn").onclick = addServer;
$("welcomeServer").onclick = addServer;

// ---- A working copy ------------------------------------------------------------------

async function openWc(path) {
  if (!(state.view && state.view.type === "wc" && state.view.path === path)) {
    state.checked = new Set();
    state.known = new Set();
    state.diffPath = null;
    state.log = [];
    state.logRev = null;
    state.incoming = false;
    $("commitMsg").value = "";
    $("diff").replaceChildren();
    $("diffTitle").textContent = "Pick a file to see its changes";
    $("openFileBtn").hidden = true;
  }
  state.view = { type: "wc", path };
  store("last", JSON.stringify(state.view));
  const wc = state.wcs.find(w => w.path === path);
  $("wcName").textContent = wc ? wc.name : path;
  $("wcUrl").textContent = wc ? wc.url : "";
  $("wcRev").textContent = "";
  $("wcIncoming").hidden = true;
  renderSidebar();
  showView("wc");
  setTab(currentTab());
  await refreshWc(false);
}

async function refreshWc(remote) {
  const path = state.view.path;
  try {
    const [info, st] = await Promise.all([call("Info", path), call("Status", path, remote)]);
    if (!state.view || state.view.path !== path) return;
    state.info = info;
    $("wcUrl").textContent = info.relativeUrl || info.url;
    $("wcUrl").title = info.url;
    $("wcRev").textContent = "r" + info.revision;
    state.changes = st.changes;
    if (remote) {
      state.incoming = true;
      const n = st.changes.filter(c => c.repoItem).length;
      $("wcIncoming").textContent = n ? `${n} incoming (HEAD r${st.head})` : `up to date with r${st.head}`;
      $("wcIncoming").hidden = false;
    }
    renderChanges();
    if (currentTab() === "history" && !state.log.length) loadLog(true);
  } catch (e) {
    report(e);
  }
}

function stInfo(ch) {
  const items = {
    modified: "M", added: "A", deleted: "D", replaced: "R", unversioned: "?", missing: "!",
    conflicted: "C", obstructed: "~", external: "X", incomplete: "!", ignored: "I",
  };
  if (ch.treeConflict) return { letter: "C", cls: "conflicted", label: "tree conflict" };
  if (ch.props === "conflicted") return { letter: "C", cls: "conflicted", label: "property conflict" };
  if (items[ch.item]) return { letter: items[ch.item], cls: ch.item, label: ch.item };
  if (ch.props === "modified") return { letter: "P", cls: "props", label: "properties modified" };
  if (ch.locked) return { letter: "L", cls: "missing", label: "locked: run Cleanup" };
  return { letter: "", cls: "", label: "" };
}

// localChange says whether the row has something to commit or revert (not
// just an incoming change from the server).
function localChange(ch) {
  return ch.item !== "normal" && ch.item !== "external" && ch.item !== "none" || ch.props === "modified" || ch.props === "conflicted" || ch.treeConflict;
}

function visibleChanges() {
  const showUnv = $("showUnversioned").checked;
  return state.changes.filter(c => showUnv || c.item !== "unversioned");
}

function renderChanges() {
  const rows = visibleChanges();
  for (const c of state.changes) {
    if (!state.known.has(c.path)) {
      state.known.add(c.path);
      if (localChange(c) && c.item !== "unversioned") state.checked.add(c.path);
    }
  }
  const paths = new Set(state.changes.map(c => c.path));
  for (const p of state.checked) if (!paths.has(p)) state.checked.delete(p);

  const tb = $("changes");
  tb.replaceChildren();
  for (const ch of rows) {
    const si = stInfo(ch);
    const can = localChange(ch);
    const box = el("input", { type: "checkbox", disabled: !can, "aria-label": "Include " + ch.path });
    box.checked = can && state.checked.has(ch.path);
    box.onclick = e => {
      e.stopPropagation();
      if (box.checked) state.checked.add(ch.path); else state.checked.delete(ch.path);
      updateChangeButtons();
    };
    const tr = el("tr", { class: ch.path === state.diffPath ? "sel" : "", onclick: () => showDiff(ch) },
      el("td", {}, box),
      el("td", {}, el("span", { class: "st st-" + si.cls, title: si.label }, si.letter)),
      el("td", {}, ch.repoItem ? el("span", { class: "incoming", title: "changed on the server: " + ch.repoItem }, "*") : ""),
      el("td", { class: "path", title: ch.path + (ch.changelist ? "  (changelist " + ch.changelist + ")" : "") },
        ch.path + (ch.isDir ? "/" : "")));
    tr.ondblclick = () => Go().Open(state.view.path, ch.path);
    tb.append(tr);
  }
  if (!rows.length) tb.append(el("tr", {}, el("td", { colspan: 4, class: "placeholder" }, "No local changes")));
  const local = state.changes.filter(localChange).length;
  $("changeCount").textContent = local === 1 ? "1 change" : local + " changes";
  updateChangeButtons();
}

function checkedChanges() {
  return state.changes.filter(c => state.checked.has(c.path) && localChange(c));
}

function updateChangeButtons() {
  const sel = checkedChanges();
  const rows = visibleChanges().filter(localChange);
  $("selAll").checked = rows.length > 0 && rows.every(c => state.checked.has(c.path));
  $("commitBtn").disabled = !sel.length;
  $("revertBtn").disabled = !sel.some(c => c.item !== "unversioned");
  $("addBtn").disabled = !sel.some(c => c.item === "unversioned");
  $("resolveBox").hidden = !state.changes.some(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict);
}

$("selAll").onchange = () => {
  for (const c of visibleChanges().filter(localChange)) {
    if ($("selAll").checked) state.checked.add(c.path); else state.checked.delete(c.path);
  }
  renderChanges();
};
$("showUnversioned").onchange = () => {
  store("showUnversioned", $("showUnversioned").checked ? "1" : "0");
  renderChanges();
};

// ---- Diffs -------------------------------------------------------------------------------

const MAX_DIFF_LINES = 20000;

function renderDiff(target, text) {
  target.replaceChildren();
  if (!text || !text.trim()) {
    target.append(el("div", { class: "placeholder" }, "No text changes to show."));
    return;
  }
  const lines = text.split("\n");
  const frag = document.createDocumentFragment();
  for (const l of lines.slice(0, MAX_DIFF_LINES)) {
    let cls = "";
    if (l.startsWith("+++") || l.startsWith("---") || l.startsWith("Index:") || l.startsWith("====") ||
        l.startsWith("Property changes on") || l.startsWith("Cannot display")) cls = "meta";
    else if (l.startsWith("@@")) cls = "hunk";
    else if (l.startsWith("+")) cls = "add";
    else if (l.startsWith("-")) cls = "del";
    frag.append(el("div", { class: cls }, l));
  }
  if (lines.length > MAX_DIFF_LINES) frag.append(el("div", { class: "meta" }, `… ${lines.length - MAX_DIFF_LINES} more lines not shown`));
  target.append(frag);
}

async function showDiff(ch) {
  state.diffPath = ch.path;
  for (const tr of $("changes").children) tr.classList.remove("sel");
  const idx = visibleChanges().indexOf(ch);
  if (idx >= 0 && $("changes").children[idx]) $("changes").children[idx].classList.add("sel");
  $("diffTitle").textContent = ch.path;
  $("openFileBtn").hidden = false;
  const path = state.view.path;
  $("diff").replaceChildren(el("div", { class: "placeholder" }, "Loading…"));
  try {
    const d = await call("Diff", path, ch.path);
    if (state.diffPath === ch.path && state.view.path === path) renderDiff($("diff"), d);
  } catch (e) {
    $("diff").replaceChildren(el("div", { class: "placeholder" }, parseErr(e).message));
  }
}
$("openFileBtn").onclick = () => state.diffPath && Go().Open(state.view.path, state.diffPath);

// ---- Commit, revert, add, resolve ----------------------------------------------------------

async function commit() {
  const sel = checkedChanges();
  const msg = $("commitMsg").value;
  if (!sel.length) return toast("Tick the changes to commit.");
  if (!msg.trim()) { $("commitMsg").focus(); return toast("Write a commit message first."); }
  await busy($("commitBtn"), async () => {
    const rev = await call("Commit", state.view.path, msg, sel.map(c => c.path));
    $("commitMsg").value = "";
    state.log = [];
    toast(rev ? `Committed revision ${rev}.` : "Committed.", "ok");
    await refreshWc(false);
  });
}
$("commitBtn").onclick = commit;
$("commitMsg").addEventListener("keydown", e => {
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) { e.preventDefault(); commit(); }
});

$("revertBtn").onclick = async () => {
  const sel = checkedChanges().filter(c => c.item !== "unversioned");
  if (!sel.length) return;
  const list = sel.slice(0, 12).map(c => "  " + c.path).join("\n") + (sel.length > 12 ? `\n  … and ${sel.length - 12} more` : "");
  if (!await confirmDanger("Revert changes?", `Your local changes to these will be lost:\n\n${list}`, "Revert")) return;
  await busy($("revertBtn"), async () => {
    await call("Revert", state.view.path, sel.map(c => c.path));
    toast(`Reverted ${sel.length} item${sel.length === 1 ? "" : "s"}.`, "ok");
    await refreshWc(false);
    if (state.diffPath) { const c = state.changes.find(c => c.path === state.diffPath); if (c) showDiff(c); else $("diff").replaceChildren(); }
  });
};

$("addBtn").onclick = () => busy($("addBtn"), async () => {
  const sel = checkedChanges().filter(c => c.item === "unversioned");
  await call("Add", state.view.path, sel.map(c => c.path));
  await refreshWc(false);
});

$("resolveBtn").onclick = () => busy($("resolveBtn"), async () => {
  const sel = checkedChanges().filter(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict);
  if (!sel.length) return toast("Tick the conflicted files to resolve.");
  await call("Resolve", state.view.path, $("resolveHow").value, sel.map(c => c.path));
  toast(`Resolved ${sel.length} conflict${sel.length === 1 ? "" : "s"}.`, "ok");
  await refreshWc(false);
});

// ---- Working copy buttons ----------------------------------------------------------------

$("updateBtn").onclick = () => busy($("updateBtn"), async () => {
  const wc = state.view.path;
  try {
    await withProgress("Updating " + $("wcName").textContent, () => call("Update", wc));
  } finally {
    state.log = [];
    await refreshWc(state.incoming);
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

document.addEventListener("keydown", e => {
  if (e.key === "F5") {
    e.preventDefault();
    if (state.view && state.view.type === "wc") refreshWc(state.incoming);
    else if (state.view && state.view.type === "browse") listBrowse();
  }
});

// ---- Tabs and history ------------------------------------------------------------------------

function currentTab() { return store("tab") === "history" ? "history" : "changes"; }

function setTab(tab) {
  store("tab", tab);
  for (const b of document.querySelectorAll(".tabs button")) b.classList.toggle("active", b.dataset.tab === tab);
  $("tab-changes").hidden = tab !== "changes";
  $("tab-history").hidden = tab !== "history";
  if (tab === "history" && !state.log.length && state.info) loadLog(true);
}
for (const b of document.querySelectorAll(".tabs button")) b.onclick = () => setTab(b.dataset.tab);

const LOG_PAGE = 100;

async function loadLog(fresh) {
  if (!state.info) return;
  const url = state.info.url;
  const from = fresh || !state.log.length ? 0 : state.log[state.log.length - 1].revision - 1;
  if (!fresh && from < 1) return;
  try {
    const entries = await call("Log", url, from, LOG_PAGE);
    if (!state.info || state.info.url !== url) return;
    state.log = fresh ? entries : state.log.concat(entries);
    renderLog();
    $("moreLog").hidden = entries.length < LOG_PAGE;
  } catch (e) { report(e); }
}
$("moreLog").onclick = () => loadLog(false);

function renderLog() {
  const tb = $("log");
  tb.replaceChildren();
  for (const e of state.log) {
    const first = (e.message || "").split("\n")[0];
    tb.append(el("tr", { class: e.revision === state.logRev ? "sel" : "", onclick: () => showRevision(e) },
      el("td", { class: "muted" }, "r" + e.revision),
      el("td", {}, e.author || ""),
      el("td", { class: "muted" }, fmtDate(e.date)),
      el("td", { class: "msg", title: e.message }, first)));
  }
  if (!state.log.length) tb.append(el("tr", {}, el("td", { class: "placeholder" }, "No history")));
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
    el("div", { class: "row" }, el("button", { onclick: () => pick(null, root + "@" + e.revision, e.revision) }, "Show all changes")),
    paths);
  $("revDiff").replaceChildren(el("div", { class: "placeholder" }, "Pick a file to see what this revision changed in it."));
}

// ---- Repository browser ------------------------------------------------------------------------

// openBrowse shows url; server is the saved repository it came from (null
// when browsing from a working copy).
async function openBrowse(server, url, name) {
  state.view = { type: "browse", server: name === null ? null : server, url, root: null, name };
  if (name !== null) store("last", JSON.stringify(state.view));
  $("browseName").textContent = name || "Repository";
  $("removeServerBtn").hidden = name === null;
  renderSidebar();
  showView("browse");
  $("entries").replaceChildren(el("tr", {}, el("td", { class: "placeholder", colspan: 5 }, "Loading…")));
  try {
    const info = await call("RepoInfo", url);
    if (state.view.url !== url) return;
    state.view.root = info.repoRoot;
    if (name === null) $("browseName").textContent = info.repoRoot.split("/").pop();
  } catch (e) {
    report(e);
    $("entries").replaceChildren();
    return;
  }
  listBrowse();
}

function renderCrumbs() {
  const { root, url } = state.view;
  const c = $("crumbs");
  c.replaceChildren();
  const rest = url.startsWith(root) ? url.slice(root.length).split("/").filter(Boolean) : [];
  const go = u => () => { state.view.url = u; listBrowse(); };
  c.append(el("button", { class: "ghost", onclick: go(root) }, decodeURIComponent(root.split("/").pop() || root)));
  let u = root;
  for (const part of rest) {
    u += "/" + part;
    c.append(el("span", { class: "muted" }, "/"), el("button", { class: "ghost", onclick: go(u) }, decodeURIComponent(part)));
  }
}

async function listBrowse() {
  const url = state.view.url;
  renderCrumbs();
  try {
    const entries = await call("List", url);
    if (state.view.url !== url) return;
    const tb = $("entries");
    tb.replaceChildren();
    for (const e of entries) {
      const dir = e.kind === "dir";
      const tr = el("tr", {},
        el("td", {}, el("span", { class: dir ? "" : "muted" }, (dir ? "▸ " : "  ") + e.name + (dir ? "/" : ""))),
        el("td", { class: "muted" }, e.commit.revision ? "r" + e.commit.revision : ""),
        el("td", {}, e.commit.author || ""),
        el("td", { class: "muted" }, fmtDate(e.commit.date)),
        el("td", { class: "muted" }, dir ? "" : fmtSize(e.size)));
      if (dir) {
        tr.style.cursor = "pointer";
        tr.onclick = () => { state.view.url = url.replace(/\/+$/, "") + "/" + encodeURIComponent(e.name); listBrowse(); };
      }
      tb.append(tr);
    }
    if (!entries.length) tb.append(el("tr", {}, el("td", { class: "placeholder", colspan: 5 }, "Empty folder")));
  } catch (e) { report(e); }
}

$("removeServerBtn").onclick = async () => {
  if (!state.view.server) return;
  if (!await confirmDanger("Remove repository?", `${state.view.name} leaves the sidebar. Nothing changes on the server.`, "Remove")) return;
  await Go().RemoveServer(state.view.server);
  state.view = null;
  store("last", "");
  await reloadLists();
  showView("welcome");
};

// ---- Checkout -----------------------------------------------------------------------------------

function coTarget() {
  const parent = $("coParent").value.trim().replace(/[\\/]+$/, "");
  const name = $("coName").value.trim();
  return parent && name ? parent + sep() + name : "";
}
function showCoTarget() {
  const t = coTarget();
  $("coTarget").textContent = t ? "Checks out into " + t : "Pick a folder and a name.";
}
$("coParent").oninput = showCoTarget;
$("coName").oninput = showCoTarget;
$("coPick").onclick = async () => {
  const d = await Go().PickFolder("Check out into…");
  if (d) { $("coParent").value = d; showCoTarget(); }
};

// folderName suggests a local folder name for a repository URL: trunk or a
// branch is named after its project.
function folderName(url) {
  const parts = url.split("/").filter(Boolean).map(decodeURIComponent);
  let name = parts[parts.length - 1] || "checkout";
  if ((name === "trunk" || parts[parts.length - 2] === "branches") && parts.length >= 2) {
    const proj = parts[parts.length - (name === "trunk" ? 2 : 3)];
    if (proj) name = name === "trunk" ? proj : proj + "-" + name;
  }
  return name;
}

// askTarget opens the checkout dialog (also used for export) and resolves
// with the chosen local folder, or "" if canceled.
async function askTarget(title, note, okLabel) {
  const url = state.view.url;
  $("coTitle").textContent = title;
  $("coNote").textContent = note;
  $("coOk").textContent = okLabel;
  $("coUrl").value = url;
  $("coParent").value = store("checkoutParent") || "";
  $("coName").value = folderName(url);
  showCoTarget();
  if (await ask($("checkoutDlg")) !== "ok") return "";
  const target = coTarget();
  if (!target) { toast("Pick a folder and a name.", "err"); return ""; }
  store("checkoutParent", $("coParent").value.trim());
  return target;
}

$("checkoutHereBtn").onclick = async () => {
  const target = await askTarget("Check out", "A working copy: you can change files and commit them back to this repository.", "Check out");
  if (!target) return;
  try {
    const wc = await withProgress("Checking out " + $("coName").value, () => call("Checkout", $("coUrl").value.trim(), target));
    $("progDlg").close();
    await reloadLists();
    toast("Checked out into " + wc.path, "ok");
    openWc(wc.path);
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
  }
};

$("exportHereBtn").onclick = async () => {
  const target = await askTarget("Export",
    "Just the files: a plain copy with no link to this repository. You can't commit from it, and nothing you do to it touches the repository.",
    "Export");
  if (!target) return;
  try {
    await withProgress("Exporting " + $("coName").value, () => call("Export", $("coUrl").value.trim(), target));
    $("progDlg").close();
    toast("Exported into " + target, "ok");
    Go().Open(target, "");
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
  }
};

// ---- Import -------------------------------------------------------------------------------------

function imTarget() {
  const name = $("imName").value.trim().replace(/^\/+|\/+$/g, "");
  return name ? state.view.url.replace(/\/+$/, "") + "/" + encodePath(name) : "";
}
function showImTarget() {
  const t = imTarget();
  $("imTarget").textContent = t ? "Creates " + decodeURI(t) : "Name the new repository folder.";
}
$("imName").oninput = showImTarget;
$("imPick").onclick = async () => {
  const d = await Go().PickFolder("Folder to import");
  if (!d) return;
  $("imDir").value = d;
  const base = d.split(/[\\/]/).filter(Boolean).pop() || "";
  if (!$("imName").value.trim()) $("imName").value = base;
  if (!$("imMsg").value.trim()) $("imMsg").value = "Import " + base;
  showImTarget();
};

$("importHereBtn").onclick = async () => {
  $("imDir").value = "";
  $("imName").value = "";
  $("imMsg").value = "";
  showImTarget();
  if (await ask($("importDlg")) !== "ok") return;
  const dir = $("imDir").value.trim(), url = imTarget(), msg = $("imMsg").value.trim();
  if (!dir || !url) return toast("Pick a folder and name the new repository folder.", "err");
  if (!msg) return toast("Write a commit message.", "err");
  const inPlace = $("imInPlace").checked;
  try {
    const rev = await withProgress("Importing " + dir, () => call("Import", dir, url, msg, inPlace));
    $("progDlg").close();
    toast(`Imported as revision ${rev}.` + (inPlace ? ` ${dir} is now a working copy.` : ""), "ok");
    if (inPlace) {
      await reloadLists();
      // svn reports the working copy by its absolute path, which may be spelled differently
      const trim = p => p.replace(/[\\/]+$/, "");
      const wc = state.wcs.find(w => trim(w.path) === trim(dir)) || state.wcs[state.wcs.length - 1];
      if (wc) openWc(wc.path);
    } else {
      listBrowse();
    }
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
    await reloadLists();
    if (state.view && state.view.type === "browse") listBrowse();
  }
};

// ---- Updates ------------------------------------------------------------------------------------

let updateInfo = null;

// showUpdate shows the banner for a newer release, unless the user said
// "Later" to that version (force: a check they asked for).
function showUpdate(info, force) {
  if (!info || !info.available) return;
  if (!force && store("updateLater") === info.latest) return;
  updateInfo = info;
  $("updateTitle").textContent = `TrunkLine ${info.latest} is available.`;
  $("updateNotes").textContent = (info.notes || []).slice(0, 3).join("  ·  ") +
    (info.canInstall ? "" : "  (built from source: pull and rebuild to update)");
  $("updateInstall").hidden = !info.canInstall;
  $("updateBanner").hidden = false;
}
window.runtime.EventsOn("update:available", info => showUpdate(info, false));
window.runtime.EventsOn("update:progress", line => {
  progLines.push(line);
  if (!progFlush) progFlush = setTimeout(flushProgress, 50);
});

$("updateLater").onclick = () => {
  if (updateInfo) store("updateLater", updateInfo.latest);
  $("updateBanner").hidden = true;
};
$("updatePage").onclick = () => updateInfo && Go().OpenReleasePage(updateInfo.url).catch(report);
$("updateInstall").onclick = async () => {
  if (!updateInfo) return;
  $("progCancel").hidden = true; // an install can't be stopped halfway
  try {
    // On success the app quits and the new version starts.
    await withProgress("Updating to " + updateInfo.latest, () => call("InstallUpdate", updateInfo.latest));
  } catch (e) {
    report(e);
  } finally {
    $("progCancel").hidden = false;
  }
};

function describeUpdate(info) {
  if (info.available) return info.canInstall ? `${info.latest} is available` : `${info.latest} is available: pull and rebuild`;
  if (info.note) return info.note + (info.latest ? ` (latest release: ${info.latest})` : "");
  return info.latest ? `Up to date (latest release: ${info.latest})` : "No releases published yet";
}

$("version").onclick = async () => {
  const s = state.startup;
  const kind = await Go().InstallKind();
  $("aboutVersion").textContent = `${s.version} (${{ deb: ".deb package", windows: "Windows setup", source: "built from source" }[kind] || kind})`;
  $("aboutSvn").textContent = s.svnError ? "not found" : `${s.svnVersion} (${s.svnPath})`;
  $("aboutAuto").checked = s.updateCheck;
  ask($("aboutDlg"));
};
$("aboutAuto").onchange = async () => {
  const on = $("aboutAuto").checked;
  try {
    await Go().SetUpdateCheck(on);
    state.startup.updateCheck = on;
  } catch (e) { report(e); }
};
$("aboutCheck").onclick = () => busy($("aboutCheck"), async () => {
  $("aboutUpdate").textContent = "Checking…";
  try {
    const info = await call("CheckUpdate");
    $("aboutUpdate").textContent = describeUpdate(info);
    showUpdate(info, true);
  } catch (e) {
    $("aboutUpdate").textContent = parseErr(e).message;
  }
});
$("aboutRepo").onclick = async () => Go().OpenReleasePage(await Go().RepoURL()).catch(report);

// ---- Start ------------------------------------------------------------------------------------

async function start() {
  const s = await Go().Startup();
  state.startup = s;
  state.wcs = s.workingCopies;
  state.servers = s.servers;
  $("version").textContent = s.version;
  if (s.svnError) {
    $("svnChip").textContent = "svn missing";
    $("svnChip").className = "chip err";
    const win = s.os === "windows";
    $("noSvn").textContent = "The svn command-line client was not found: " + s.svnError + ". " + (win
      ? "Install TortoiseSVN and tick \"command line client tools\" in its installer (or install SlikSVN), then restart TrunkLine."
      : "Install it (Ubuntu: sudo apt install subversion), then restart TrunkLine.");
    $("noSvn").hidden = false;
  } else {
    $("svnChip").textContent = "svn " + s.svnVersion;
    $("svnChip").className = "chip ok";
    $("svnChip").title = s.svnPath;
  }
  $("showUnversioned").checked = store("showUnversioned") !== "0";
  renderSidebar();

  let last = null;
  try { last = JSON.parse(store("last") || "null"); } catch { /* ignore */ }
  if (last && last.type === "wc" && state.wcs.some(w => w.path === last.path)) openWc(last.path);
  else if (last && last.type === "browse" && state.servers.some(x => x.url === last.server)) openBrowse(last.server, last.server, last.name);
  else showView("welcome");
}

start();
