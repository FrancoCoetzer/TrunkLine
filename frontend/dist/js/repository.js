"use strict";
// TrunkLine's page: repository browser; changing the repository from the browser; checkout; import.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

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
    const hadFocus = tb.contains(document.activeElement);
    tb.replaceChildren();
    for (const e of entries) {
      const dir = e.kind === "dir";
      const tr = el("tr", {},
        el("td", {}, el("span", { class: dir ? "" : "muted" }, (dir ? "▸ " : "  ") + e.name + (dir ? "/" : "")),
          e.lock ? lockMark("other", e.lock) : null),
        el("td", { class: "muted" }, e.commit.revision ? "r" + e.commit.revision : ""),
        el("td", {}, e.commit.author || ""),
        el("td", { class: "muted" }, fmtDate(e.commit.date)),
        el("td", { class: "muted" }, dir ? "" : fmtSize(e.size)));
      tr.style.cursor = "pointer";
      const child = url.replace(/\/+$/, "") + "/" + encodeURIComponent(e.name);
      const more = el("button", { type: "button", class: "ghost", "aria-label": "More for " + e.name, title: "Rename, move, delete, copy URL",
        "aria-haspopup": "menu", "aria-expanded": "false" }, "⋯");
      more.onclick = ev => {
        ev.stopPropagation();
        state.entry = { url: child, name: e.name, dir };
        const m = $("entryMenu");
        if (m.matches(":popover-open")) m.hidePopover();
        m._btn = more;
        m.showPopover();
      };
      tr.append(el("td", { class: "act" }, more));
      if (dir) {
        tr.onclick = () => { state.view.url = child; listBrowse(); };
      } else {
        tr.title = "Click to see who changed each line (blame)";
        tr.onclick = () => openBlame(() => call("BlameURL", child));
      }
      tr.dataset.key = e.name;
      tr._enter = tr.onclick;
      tb.append(tr);
    }
    if (!entries.length) tb.append(el("tr", {}, el("td", { class: "placeholder", colspan: 6 }, "Empty folder")));
    tb._lastKey = null; // a new folder: start at its first row
    rovingRows(tb, hadFocus);
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

// ---- Changing the repository from the browser --------------------------------------------------
//
// Each change is one commit straight to the repository (no working copy).

// repoPath is a repository URL from the repository root ("/trunk/docs").
function repoPath(u) {
  const root = state.view.root || "";
  return decodeURIComponent(u.startsWith(root) ? u.slice(root.length) || "/" : u);
}

// repoAct asks for a path (unless path is null) and a commit message, and
// runs act(path, message) until it works or is canceled.
async function repoAct({ title, what, pathLabel, path, okLabel, message, danger }, act) {
  $("repoActTitle").textContent = title;
  $("repoActWhat").textContent = what;
  $("repoActPathRow").hidden = path === null;
  $("repoActPathLabel").textContent = pathLabel || "";
  $("repoActPath").value = path || "";
  $("repoActMsg").value = message;
  $("repoActOk").textContent = okLabel;
  $("repoActOk").className = danger ? "danger" : "primary";
  showErrIn($("repoActErr"), null);
  for (;;) {
    const r = ask($("repoActDlg"));
    (path === null ? $("repoActMsg") : $("repoActPath")).focus();
    if (await r !== "ok") return false;
    try {
      const rev = await call(...act($("repoActPath").value.trim(), $("repoActMsg").value.trim()));
      toast(`${title}: done (r${rev}).`, "ok");
      return true;
    } catch (e) { showErrIn($("repoActErr"), e); }
  }
}

$("repoMkdirBtn").onclick = () => flow(async () => {
  const here = state.view.url.replace(/\/+$/, "");
  const ok = await repoAct({ title: "New folder", what: "In " + repoPath(here), pathLabel: "Folder name", path: "",
    okLabel: "Create", message: "" }, (name, msg) => {
    if (!name || /[\\/]/.test(name)) throw { message: "Give the folder a name (no slashes)." };
    return ["RepoMkdir", here + "/" + encodeURIComponent(name), msg || `Add folder ${repoPath(here + "/" + encodeURIComponent(name))}`];
  });
  if (ok) listBrowse();
});
$("repoCopyUrlBtn").onclick = () => copyText(state.view.url);

async function copyText(text) {
  try { await Go().CopyText(text); toast("Copied " + text, "ok"); } catch (e) { report(e); }
}

async function entryMove() {
  const e = state.entry;
  const from = repoPath(e.url);
  const ok = await repoAct({ title: "Rename or move", what: `${from}${e.dir ? "/" : ""}: its history comes along. Working copies get the change when they update.`,
    pathLabel: "New path (from the repository root)", path: from, okLabel: "Rename", message: "" }, (to, msg) => {
    to = "/" + to.replace(/^\/+|\/+$/g, "");
    if (to === "/") throw { message: "Give the new path." };
    return ["RepoMove", e.url, state.view.root + encodePath(to), msg || `Move ${from} to ${to}`];
  });
  if (ok) listBrowse();
}

async function entryDelete() {
  const e = state.entry;
  const what = repoPath(e.url) + (e.dir ? "/" : "");
  const ok = await repoAct({ title: "Delete", what: `Delete ${what} from the repository${e.dir ? ", with everything in it" : ""}? It stays in the history, so it can be brought back. Working copies lose it when they update.`,
    path: null, okLabel: "Delete", message: `Delete ${what}`, danger: true }, (_, msg) => ["RepoDelete", e.url, msg || `Delete ${what}`]);
  if (ok) listBrowse();
}

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
async function askTarget(title, note, okLabel, withDepth) {
  const url = state.view.url;
  $("coDepthRow").hidden = !withDepth;
  $("coDepth").value = "";
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
  const target = await askTarget("Check out", "A working copy: you can change files and commit them back to this repository.", "Check out", true);
  if (!target) return;
  const depth = $("coDepth").value;
  try {
    const wc = await withProgress("Checking out " + $("coName").value, () => call("Checkout", $("coUrl").value.trim(), target, depth));
    $("progDlg").close();
    await reloadLists();
    toast("Checked out into " + wc.path, "ok");
    await openWc(wc.path);
    if (depth) openFolders(); // the top folder only: choose what else
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
