"use strict";
// TrunkLine's page: renaming and moving; choosing folders (sparse working copies).
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Renaming and moving -----------------------------------------------------------------------
//
// svn move keeps a file's history. A rename done in a file manager looks to
// svn like a missing file and a new one: the hint above the change list
// spots those and records them as moves.

async function openMove(rel) {
  $("moveFrom").value = rel || "";
  $("moveTo").value = rel || "";
  showErrIn($("moveErr"), null);
  for (;;) {
    const r = ask($("moveDlg"));
    const to = $("moveTo");
    to.focus();
    // Select the name, not the folders before it: a rename is the usual case.
    const slash = to.value.lastIndexOf("/") + 1, dot = to.value.lastIndexOf(".");
    to.setSelectionRange(slash, dot > slash ? dot : to.value.length);
    if (await r !== "ok") return;
    const from = $("moveFrom").value.trim().replace(/\\/g, "/"), dest = $("moveTo").value.trim().replace(/\\/g, "/");
    try {
      await call("MoveFile", state.view.path, from, dest);
    } catch (e) { showErrIn($("moveErr"), e); continue; }
    tickMove(from, dest);
    toast(`Renamed ${from} to ${dest}. Commit to share it.`, "ok");
    await refreshWc(false);
    return;
  }
}
$("moveBtn").onclick = () => openMove(state.diffPath && state.diffPath !== "." ? state.diffPath : "");
$("moveFileBtn").onclick = () => state.diffPath && openMove(state.diffPath);
$("movePick").onclick = async () => {
  try {
    const rel = await call("PickFileIn", state.view.path, "Rename or move…");
    if (rel) { $("moveFrom").value = rel; $("moveTo").value = rel; $("moveTo").focus(); }
  } catch (e) { showErrIn($("moveErr"), e); }
};

// tickMove ticks both halves of a move: committed alone, the old name
// would just be deleted.
function tickMove(from, to) {
  for (const p of [from, to]) { state.checked.add(p); state.known.add(p); }
}

let renamePairs = [];
async function updateRenameHint() {
  const wc = state.view && state.view.path;
  const hint = $("renameHint");
  if (!wc || !state.changes.some(c => c.item === "missing") || !state.changes.some(c => c.item === "unversioned")) {
    hint.hidden = true;
    return;
  }
  try {
    const pairs = await call("RenameCandidates", wc);
    if (!state.view || state.view.path !== wc) return;
    renamePairs = pairs;
    hint.hidden = !pairs.length;
    if (!pairs.length) return;
    const list = pairs.slice(0, 3).map(p => `${p.from}${p.dir ? "/" : ""} → ${p.to}${p.dir ? "/" : ""}`).join(", ") +
      (pairs.length > 3 ? ` and ${pairs.length - 3} more` : "");
    $("renameHintText").textContent = `These look renamed or moved outside svn: ${list}. Record ${pairs.length === 1 ? "it as a rename" : "them as renames"} to keep the history.`;
  } catch { hint.hidden = true; }
}
$("renameHintBtn").onclick = () => busy($("renameHintBtn"), async () => {
  await call("RecordRenames", state.view.path, renamePairs);
  for (const p of renamePairs) tickMove(p.from, p.to);
  toast(`Recorded ${renamePairs.length === 1 ? "1 rename" : renamePairs.length + " renames"}. Commit to share ${renamePairs.length === 1 ? "it" : "them"}.`, "ok");
  $("renameHint").hidden = true;
  await refreshWc(false);
});

// ---- Choosing folders (sparse working copies) ----------------------------------------------------
//
// Ticked: checked out in full; unticked: left out of this working copy;
// a dash: partly there (open it to choose inside). Changes are collected
// across folders and applied together.

let fold = null; // {rel, want: Map(path -> true|false), depth: Map(path -> depth)}

async function openFolders() {
  fold = { rel: "", want: new Map(), depth: new Map() };
  showErrIn($("foldersErr"), null);
  loadFolders("");
  for (;;) {
    if (await ask($("foldersDlg")) !== "ok") { fold = null; return; }
    const fetch = [], remove = [];
    for (const [p, on] of fold.want) {
      const d = fold.depth.get(p) || "";
      const present = d !== "" && d !== "exclude";
      if (on && d !== "infinity") fetch.push(p);
      if (!on && present) remove.push(p);
    }
    if (!fetch.length && !remove.length) { fold = null; return; }
    try {
      await withProgress("Choosing folders", () => call("SetFolders", state.view.path, fetch, remove));
      $("progDlg").close();
    } catch (e) {
      if ($("progDlg").open) { fold = null; await refreshWc(false); return; } // svn's output says what happened
      showErrIn($("foldersErr"), e);
      continue;
    }
    fold = null;
    toast(`Done: ${fetch.length} fetched, ${remove.length} left out.`, "ok");
    await refreshWc(false);
    return;
  }
}
$("foldersBtn").onclick = () => state.view && openFolders();

async function loadFolders(rel) {
  fold.rel = rel;
  const c = $("foldersCrumbs");
  c.replaceChildren(el("button", { type: "button", class: "ghost", onclick: () => loadFolders("") }, $("wcName").textContent || "top"));
  let acc = "";
  for (const part of rel ? rel.split("/") : []) {
    acc = acc ? acc + "/" + part : part;
    const to = acc;
    c.append(el("span", { class: "muted" }, "/"), el("button", { type: "button", class: "ghost", onclick: () => loadFolders(to) }, part));
  }
  $("foldersList").replaceChildren(el("tr", {}, el("td", { class: "placeholder" }, "Asking the server…")));
  let folders;
  try {
    folders = await call("Folders", state.view.path, rel);
  } catch (e) {
    $("foldersList").replaceChildren(el("tr", {}, el("td", { class: "placeholder" }, parseErr(e).message)));
    return;
  }
  if (!fold || fold.rel !== rel) return;
  for (const f of folders) {
    const p = rel ? rel + "/" + f.name : f.name;
    if (!fold.depth.has(p)) fold.depth.set(p, f.depth);
  }
  renderFolders(folders.map(f => f.name));
}

const DEPTH_TEXT = { infinity: "checked out", immediates: "partly", files: "partly", empty: "partly", exclude: "left out", "": "not checked out" };

function renderFolders(names) {
  const tb = $("foldersList");
  tb.replaceChildren();
  if (!names.length) tb.append(el("tr", {}, el("td", { class: "placeholder" }, "No folders in here.")));
  for (const name of names) {
    const p = fold.rel ? fold.rel + "/" + name : name;
    const d = fold.depth.get(p) || "";
    const present = d !== "" && d !== "exclude";
    const box = el("input", { type: "checkbox", "aria-label": "Check out " + p });
    const want = fold.want.get(p);
    box.checked = want === undefined ? d === "infinity" : want;
    box.indeterminate = want === undefined && present && d !== "infinity";
    box.onchange = () => {
      fold.want.set(p, box.checked);
      if (box.checked === (d === "infinity") || (!box.checked && !present)) fold.want.delete(p); // back as it was
      renderFolders(names);
    };
    const changed = fold.want.has(p);
    const label = changed ? (fold.want.get(p) ? "will be checked out" : "will be left out") : DEPTH_TEXT[d] || d;
    const tr = el("tr", { class: changed ? "changed" : "" },
      el("td", {}, box),
      el("td", { class: "name" }, name + "/"),
      el("td", { class: "state" }, label),
      el("td", {}, el("button", { type: "button", class: "ghost", title: "Choose the folders inside it", onclick: () => loadFolders(p) }, "Open")));
    tr.dataset.key = p;
    tr._space = () => box.click();
    tr._enter = () => loadFolders(p);
    tb.append(tr);
  }
  rovingRows(tb, false);
  let get = 0, drop = 0;
  for (const [q, on] of fold.want) {
    const d = fold.depth.get(q) || "";
    if (on) get++;
    else if (d !== "" && d !== "exclude") drop++;
  }
  $("foldersSummary").textContent = get || drop ? `To apply: ${get} to check out, ${drop} to leave out.` : "";
}
