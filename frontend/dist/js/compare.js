"use strict";
// TrunkLine's page: comparing.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Comparing ---------------------------------------------------------------------------------
//
// What differs between two places in the repository: two tags, a branch and
// trunk, or one folder (or file) at two revisions. The compare window lists
// the changed files; picking one shows its diff.

// repoLabel shows a repository URL from the repository root (^/trunk).
function repoLabel(url) {
  const root = state.info && state.info.repoRoot;
  return readable(root && url.startsWith(root) ? "^" + url.slice(root.length) : url);
}
function branchLabel(br) { return br.kind === "trunk" ? "trunk" : `${br.kind} ${br.name}`; }

// openCompare asks what to compare from ({url, rev (0: the latest), label}) with.
async function openCompare(from) {
  const isFile = !!(state.historyFile && from.url === state.historyFile.url);
  if (!isFile && state.view && state.view.type === "wc" && !state.branches) await loadBranches();
  const opts = [];
  if (from.rev) opts.push({ value: "head", label: `The latest ${repoLabel(from.url)}`, to: { url: from.url, rev: 0, label: `the latest ${repoLabel(from.url)}` } });
  opts.push({ value: "rev", label: `Another revision of ${repoLabel(from.url)}…` });
  if (!isFile && state.branches) {
    state.branches.branches.forEach((br, i) => {
      if (br.url === from.url) return; // itself (its latest is offered above)
      opts.push({ value: "br" + i, label: branchLabel(br) + (br.current ? " (your working copy is on it)" : ""), to: { url: br.url, rev: 0, label: branchLabel(br) } });
    });
  }
  $("cmpFrom").textContent = `Compare ${from.label} with:`;
  $("cmpWith").replaceChildren(...opts.map(o => el("option", { value: o.value }, o.label)));
  const showRev = () => { $("cmpRevRow").hidden = $("cmpWith").value !== "rev"; };
  $("cmpWith").onchange = () => { showRev(); if (!$("cmpRevRow").hidden) $("cmpRev").focus(); };
  $("cmpRev").value = "";
  // Start on the likeliest choice: for a revision, the latest; for a
  // branch or tag, the line the working copy is on (or the first other).
  const current = opts.find(o => o.value.startsWith("br") && state.branches.branches[Number(o.value.slice(2))].current);
  const first = from.rev ? opts[0] : current || opts.find(o => o.value.startsWith("br")) || opts[0];
  $("cmpWith").value = first.value;
  showRev();
  for (;;) {
    const r = ask($("cmpPickDlg"));
    $("cmpWith").focus();
    if (await r !== "ok") return;
    const pick = opts.find(o => o.value === $("cmpWith").value);
    let to = pick && pick.to;
    if ($("cmpWith").value === "rev") {
      const n = Number($("cmpRev").value.trim().replace(/^r/i, ""));
      if (!Number.isInteger(n) || n < 1) { toast("Type a revision number, like 120."); continue; }
      to = { url: from.url, rev: n, label: `r${n} of ${repoLabel(from.url)}` };
    }
    if (!to) return;
    // One place at two revisions: older first, so additions show as added.
    if (to.url === from.url && from.rev && to.rev && to.rev < from.rev) return showCompare(to, from);
    return showCompare(from, to);
  }
}

let cmp = null; // on show: {a, b, changes}

async function showCompare(a, b) {
  cmp = { a, b, changes: null };
  $("compareTitle").textContent = `${a.label}  →  ${b.label}`;
  $("compareTitle").title = `Changes from ${a.label} to ${b.label}`;
  $("compareStats").textContent = "";
  $("compareList").replaceChildren(el("tr", {}, el("td", { class: "placeholder" }, "Asking the server…")));
  $("compareDiff").replaceChildren();
  if (!$("compareDlg").open) ask($("compareDlg")).then(() => { cmp = null; });
  try {
    const ch = await call("Compare", a.url, a.rev, b.url, b.rev);
    if (!cmp || cmp.a !== a) return;
    cmp.changes = ch;
    renderCompare();
  } catch (e) {
    if (cmp && cmp.a === a) $("compareList").replaceChildren(el("tr", {}, el("td", { class: "placeholder" }, parseErr(e).message)));
  }
}

function renderCompare() {
  const tb = $("compareList");
  tb.replaceChildren();
  const files = cmp.changes;
  $("compareStats").textContent = files.length === 1 ? "1 change" : files.length + " changes";
  if (!files.length) {
    tb.append(el("tr", {}, el("td", { class: "placeholder" }, "No differences.")));
    $("compareDiff").replaceChildren();
    return;
  }
  for (const c of files) {
    const letter = { added: "A", deleted: "D", modified: "M", replaced: "R" }[c.item] || (c.props === "modified" ? "P" : "");
    const cls = { added: "added", deleted: "deleted", modified: "modified", replaced: "replaced" }[c.item] || "props";
    const name = c.path || (c.kind === "dir" ? "(the folder itself)" : cmp.a.label);
    const tr = el("tr", { onclick: () => showCompareDiff(c) },
      el("td", {}, el("span", { class: "st st-" + cls, title: c.item === "none" ? "properties changed" : c.item }, letter)),
      el("td", { class: "path", title: name }, name + (c.kind === "dir" && c.path ? "/" : "")));
    tr.dataset.key = c.path;
    tr._move = () => showCompareDiff(c);
    tb.append(tr);
  }
  rovingRows(tb, false);
  showCompareDiff(files[0]);
}

async function showCompareDiff(c) {
  const at = cmp;
  for (const tr of $("compareList").children) tr.classList.toggle("sel", tr.dataset.key === c.path);
  if (c.kind === "dir" && c.item !== "none" && c.item !== "modified") {
    $("compareDiff").replaceChildren(el("div", { class: "placeholder" }, `Folder ${c.item}.`));
    return;
  }
  $("compareDiff").replaceChildren(el("div", { class: "placeholder" }, "Loading…"));
  at.sel = c.path;
  try {
    const d = await call("CompareDiff", at.a.url, at.a.rev, at.b.url, at.b.rev, c.path, c.item);
    if (cmp === at && at.sel === c.path) renderDiff($("compareDiff"), d);
  } catch (e) {
    if (cmp === at && at.sel === c.path) $("compareDiff").replaceChildren(el("div", { class: "placeholder" }, parseErr(e).message));
  }
}
$("compareSwap").onclick = () => { if (cmp) showCompare(cmp.b, cmp.a); };

// tagAt makes a tag of the working copy's trunk or branch as it was at rev.
async function tagAt(rev) {
  if (!state.branches) await loadBranches();
  if (!state.branches) return;
  if (!state.branches.layout.kind) return toast("This repository doesn't use trunk / branches / tags folders, so TrunkLine doesn't know where tags go.");
  await newBranch("tag", rev);
}
