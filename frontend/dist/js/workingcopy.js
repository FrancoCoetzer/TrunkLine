"use strict";
// TrunkLine's page: a working copy.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- A working copy ------------------------------------------------------------------

async function openWc(path) {
  if (!(state.view && state.view.type === "wc" && state.view.path === path)) {
    state.checked = new Set();
    state.known = new Set();
    state.diffPath = null;
    state.log = [];
    state.logRev = null;
    state.incoming = false;
    state.shownPaths = null;
    clearBanner();
    setIncoming(0);
    setTimeout(checkIncoming, 1500);
    state.locks = [];
    state.lockChecked = new Set();
    state.branches = null;
    $("commitMsg").value = "";
    $("diff").replaceChildren();
    $("diffTitle").textContent = "Pick a file to see its changes";
    $("openFileBtn").hidden = true;
    $("blameFileBtn").hidden = true;
    $("historyFileBtn").hidden = true;
    state.historyFile = null;
    state.logSearch = "";
    $("logSearch").value = "";
    // Nothing of the previous working copy may show while this one loads.
    $("changes").replaceChildren(el("tr", {}, el("td", { class: "placeholder", colspan: 4 }, "Loading…")));
    $("changeCount").textContent = "";
    for (const id of ["log", "branches", "locks", "revDiff"]) $(id).replaceChildren();
    $("revDetail").hidden = true;
    $("wcIncoming").hidden = true;
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
    $("wcUrl").textContent = readable(info.relativeUrl || info.url);
    showBranchChip(info.relativeUrl || "");
    $("wcUrl").title = info.url;
    $("wcRev").textContent = "r" + info.revision;
    state.changes = st.changes;
    if (remote) {
      state.incoming = true;
      const n = st.changes.filter(c => c.repoItem).length;
      setIncoming(n);
      $("wcIncoming").textContent = n ? `${n} incoming (HEAD r${st.head})` : `up to date with r${st.head}`;
      $("wcIncoming").hidden = false;
    }
    renderChanges();
    updateRenameHint();
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
  const hadFocus = $("changes").contains(document.activeElement);
  const rows = visibleChanges();
  // Rows that weren't listed last time get a brief highlight (not on the first listing).
  const before = state.shownPaths;
  state.shownPaths = new Set(rows.map(c => c.path));
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
  // Files in changelists come after the others, under a heading per changelist.
  const lists = [...new Set(rows.map(c => c.changelist).filter(Boolean))].sort((a, b) => a.localeCompare(b));
  const groups = lists.length ? [["", rows.filter(c => !c.changelist)], ...lists.map(n => [n, rows.filter(c => c.changelist === n)])] : [["", rows]];
  for (const [name, members] of groups) {
    if (lists.length && members.length) tb.append(groupRow(name, members));
    for (const ch of members) tb.append(changeRow(ch, before, lists.length > 0));
  }
  if (!rows.length) tb.append(el("tr", {}, el("td", { colspan: 4, class: "placeholder" }, "No local changes")));
  rovingRows(tb, hadFocus);
  afterChanges();
}

// groupRow is the heading of a changelist in the Changes list (name "": the
// files in none), with a box that ticks the whole group.
function groupRow(name, members) {
  const can = members.filter(localChange);
  const box = el("input", { type: "checkbox", disabled: !can.length, "aria-label": "Include every file " + (name ? "in changelist " + name : "not in a changelist") });
  box.checked = can.length > 0 && can.every(c => state.checked.has(c.path));
  box.indeterminate = !box.checked && can.some(c => state.checked.has(c.path));
  box.onclick = e => {
    e.stopPropagation();
    for (const c of can) if (box.checked) state.checked.add(c.path); else state.checked.delete(c.path);
    renderChanges();
  };
  box._sync = () => {
    box.checked = can.length > 0 && can.every(c => state.checked.has(c.path));
    box.indeterminate = !box.checked && can.some(c => state.checked.has(c.path));
  };
  const n = members.length === 1 ? "1 file" : members.length + " files";
  const cells = [el("td", {}, box), el("td", { colspan: 3 },
    name ? el("span", { class: "clname" }, name) : el("span", {}, "Not in a changelist"), name ? n : "")];
  if (name) {
    cells[1].append(el("button", {
      type: "button", class: "ghost", title: "Take every file out of this changelist (the changes stay)",
      onclick: e => { e.stopPropagation(); setChangelist(members, ""); },
    }, "Ungroup"));
  }
  const tr = el("tr", { class: "group" }, ...cells);
  tr._sync = box._sync;
  tr.dataset.key = "changelist:" + name;
  tr._space = () => box.click();
  return tr;
}

function changeRow(ch, before, grouped) {
  const si = stInfo(ch);
  const can = localChange(ch);
  const box = el("input", { type: "checkbox", disabled: !can, "aria-label": "Include " + ch.path });
  box.checked = can && state.checked.has(ch.path);
  box.onclick = e => {
    e.stopPropagation();
    if (box.checked) state.checked.add(ch.path); else state.checked.delete(ch.path);
    updateChangeButtons();
  };
  const fresh = before && !before.has(ch.path) ? " fresh" : "";
  const tr = el("tr", { class: (ch.path === state.diffPath ? "sel" : "") + fresh + (grouped && ch.changelist ? " ingroup" : ""), onclick: () => showDiff(ch) },
    el("td", {}, box),
    el("td", {}, el("span", { class: "st st-" + si.cls, title: si.label, "aria-hidden": "true" }, si.letter),
      el("span", { class: "sr-only" }, si.label)),
    el("td", {}, ch.repoItem ? el("span", { class: "incoming", title: "changed on the server: " + ch.repoItem }, "*") : ""),
    el("td", { class: "path", title: ch.path + (ch.changelist ? "  (changelist " + ch.changelist + ")" : "") },
      ch.path + (ch.isDir ? "/" : ""), lockMark(ch.lockState, ch.lock)));
  tr.ondblclick = () => Go().Open(state.view.path, ch.path);
  tr.dataset.key = ch.path;
  tr._move = () => showDiff(ch);
  if (can) tr._space = () => box.click();
  tr._enter = () => Go().Open(state.view.path, ch.path);
  return tr;
}

function afterChanges() {
  // The file whose diff is shown has gone from the list (committed, reverted, ignored): clear the diff.
  if (state.diffPath && !state.changes.some(c => c.path === state.diffPath)) {
    state.diffPath = null;
    $("diff").replaceChildren();
    $("diffTitle").textContent = "Pick a file to see its changes";
    for (const id of ["openFileBtn", "blameFileBtn", "historyFileBtn", "propsFileBtn", "moveFileBtn", "mergeFileBtn"]) $(id).hidden = true;
  }
  // The banner's guidance is done with once there's nothing left to act on.
  if (state.banner && state.banner.kind === "conflict" && !state.changes.some(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict)) clearBanner();
  if (state.banner && !state.changes.some(c => localChange(c) && c.item !== "unversioned")) clearBanner();
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
  for (const tr of $("changes").querySelectorAll(":scope > tr.group")) tr._sync();
  $("commitBtn").disabled = !sel.length;
  $("revertBtn").disabled = !sel.some(c => c.item !== "unversioned");
  $("addBtn").disabled = !sel.some(c => c.item === "unversioned");
  $("ignoreBtn").disabled = !sel.some(c => c.item === "unversioned");
  $("changelistBtn").disabled = !sel.some(canChangelist);
  $("resolveBox").hidden = !state.changes.some(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict);
  $("keepLocksBox").hidden = !sel.some(c => c.lockState === "mine");
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
