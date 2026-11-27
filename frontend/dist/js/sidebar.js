"use strict";
// TrunkLine's page: sidebar.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Sidebar -------------------------------------------------------------------------

// shortPath shows a folder for the sidebar: the home folder as ~, and a long
// path shortened in the middle so its last folders (the name) stay visible.
function shortPath(p) {
  const home = state.startup && state.startup.home;
  if (home && (p === home || p.startsWith(home + sep()))) p = "~" + p.slice(home.length);
  const parts = p.split(/[\\/]/);
  if (p.length <= 34 || parts.length <= 3) return p;
  return parts[0] + sep() + "…" + sep() + parts.slice(-2).join(sep());
}

function renderSidebar() {
  const wcList = $("wcList");
  wcList.replaceChildren();
  if (!state.wcs.length) wcList.append(el("div", { class: "empty" }, "None yet"));
  for (const wc of state.wcs) {
    const active = state.view && state.view.type === "wc" && state.view.path === wc.path;
    wcList.append(el("button", { class: "navitem" + (active ? " active" : ""), title: wc.path, "aria-current": active ? "page" : null,
      onclick: () => openWc(wc.path) }, wc.name, el("span", { class: "sub" }, shortPath(wc.path))));
  }
  const sl = $("serverList");
  sl.replaceChildren();
  if (!state.servers.length) sl.append(el("div", { class: "empty" }, "None yet"));
  for (const s of state.servers) {
    const active = state.view && state.view.type === "browse" && state.view.server === s.url;
    sl.append(el("button", { class: "navitem" + (active ? " active" : ""), title: s.url, "aria-current": active ? "page" : null,
      onclick: () => openBrowse(s.url, s.url, s.name) },
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
