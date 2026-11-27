"use strict";
// TrunkLine's page: merging; undo a commit; branches.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Merging -------------------------------------------------------------------------------------

async function openMerge(br) {
  const wc = state.view.path;
  const here = state.branches.branches.find(x => x.current);
  const hereName = here ? here.name : "your working copy";
  let plan;
  try { plan = await call("MergePlan", wc, br.url); } catch (e) { return report(e); }
  if (!plan.total) return toast(`Nothing to merge: you already have everything from ${br.name}.`, "ok");

  $("mergeTitle").textContent = `Merge ${br.name} into ${hereName}`;
  $("mergeWhat").textContent = br.kind === "trunk"
    ? `Bring trunk's newer changes into ${hereName}, to keep it up to date.`
    : here && here.kind === "trunk"
      ? `Bring the work done on ${br.name} into trunk.`
      : `Bring changes from ${br.name} into ${hereName}.`;
  const tb = $("mergeRevs");
  tb.replaceChildren();
  for (const e of plan.revisions) {
    const box = el("input", { type: "checkbox", "data-rev": e.revision, "aria-label": `r${e.revision}` });
    box.checked = true;
    box.onchange = updateMergeHow;
    tb.append(mergeRow(box,
      el("td", {}, box),
      el("td", { class: "muted" }, "r" + e.revision),
      el("td", {}, e.author || ""),
      el("td", { class: "muted" }, fmtDate(e.date)),
      el("td", { class: "msg", title: e.message }, (e.message || "").split("\n")[0])));
  }
  rovingRows(tb, false);
  $("mergeCount").textContent = `${plan.total} commit${plan.total === 1 ? "" : "s"} you don't have yet` +
    (plan.total > plan.revisions.length ? ` (the newest ${plan.revisions.length} are listed)` : "");
  $("mergeAll").checked = true;
  $("mergeAll").onchange = () => { for (const b of tb.querySelectorAll("input")) b.checked = $("mergeAll").checked; updateMergeHow(); };
  function updateMergeHow() {
    const boxes = [...tb.querySelectorAll("input")];
    const picked = boxes.filter(b => b.checked).length;
    $("mergeAll").checked = picked === boxes.length;
    $("mergeOk").disabled = picked === 0;
    $("mergeHow").textContent = picked === boxes.length
      ? "Merges everything not merged yet. svn keeps track, so merging again later only brings what's new."
      : `Merges only the ${picked} ticked commit${picked === 1 ? "" : "s"} (a cherry-pick). The others can still be merged later.`;
  }
  updateMergeHow();
  if (await ask($("mergeDlg")) !== "ok") return;

  const boxes = [...tb.querySelectorAll("input")];
  const picked = boxes.filter(b => b.checked).map(b => Number(b.dataset.rev)).sort((a, b) => a - b);
  // All ticked: svn's automatic merge (it knows what to skip when bringing a
  // branch back); some: just those revisions.
  const revs = picked.length === boxes.length && plan.total === boxes.length ? [] : picked;
  const run = () => withProgress(`Merging ${br.name}`, () => call("MergeInto", wc, br.url, revs));
  try {
    await run();
  } catch (err) {
    const er = parseErr(err);
    if (er.kind === "canceled") return;
    if (er.kind === "needclean") { $("progDlg").close(); return toast(er.message, "err"); }
    if (er.kind !== "needupdate") return report(er);
    $("progDlg").close();
    if (!await confirmDanger("Update first?", er.message, "Update and merge")) return;
    try {
      await withProgress("Updating " + $("wcName").textContent, () => call("Update", wc));
      await refreshWc(false);
      if (conflictsAfter("Updated")) return;
      await run();
    } catch (err2) {
      if (parseErr(err2).kind !== "canceled") report(err2);
      return;
    }
  }
  state.log = [];
  await refreshWc(false);
  // A merge is committed whole: tick all of it.
  for (const c of state.changes) if (localChange(c) && c.item !== "unversioned") state.checked.add(c.path);
  renderChanges();
  $("commitMsg").value = revs.length
    ? `Merge ${revs.map(r => "r" + r).join(", ")} from ${br.name}`
    : `Merge ${br.name} into ${hereName}`;
  if (!conflictsAfter(`Merged ${br.name}`)) {
    $("progDlg").close();
    setTab("changes");
    setBanner("info", `Merged ${br.name}. Check the changes, then commit them together: they're all ticked and the ` +
      "message is filled in. Changed your mind? Revert them.");
  }
}

// ---- Undo a commit -------------------------------------------------------------------------------

async function undoRevision(e) {
  const wc = state.view.path;
  const local = state.changes.filter(c => localChange(c) && c.item !== "unversioned").length;
  const first = (e.message || "").split("\n")[0];
  const msg = `r${e.revision}'s changes are taken back out of your working copy, as local changes you can check and then commit. ` +
    "Nothing is sent to the server until you commit." +
    (local ? `\n\nYou have ${local} uncommitted change${local === 1 ? "" : "s"}: the undo adds to them, and where they touch ` +
      "the same lines you'll get conflicts. Committing or reverting them first keeps things simple." : "");
  if (!await confirmDanger(`Undo r${e.revision}?`, msg + (first ? `\n\nr${e.revision}: ${first}` : ""), `Undo r${e.revision}`)) return;
  const run = () => withProgress(`Undoing r${e.revision}`, () => call("UndoRevision", wc, e.revision));
  try {
    await run();
  } catch (err) {
    const er = parseErr(err);
    if (er.kind === "canceled") return;
    if (er.kind !== "needupdate") return report(er);
    $("progDlg").close();
    if (!await confirmDanger("Update first?", er.message, "Update and undo")) return;
    try {
      await withProgress("Updating " + $("wcName").textContent, () => call("Update", wc));
      await refreshWc(false);
      if (conflictsAfter("Updated")) return;
      await run();
    } catch (err2) {
      if (parseErr(err2).kind !== "canceled") report(err2);
      return;
    }
  }
  state.log = [];
  await refreshWc(false);
  if (!$("commitMsg").value.trim()) $("commitMsg").value = `Undo r${e.revision}` + (first ? `: ${first}` : "");
  if (!conflictsAfter(`Undid r${e.revision}`)) {
    $("progDlg").close();
    setTab("changes");
    setBanner("info", `r${e.revision} is undone in your working copy. Check the changes, then commit them (the message is filled in).`);
  }
}

// ---- Branches ----------------------------------------------------------------------------------

// branchOf finds the trunk, branch or tag in a repository-relative URL
// (^/proj/branches/imu/src): {kind, name} or null. The Go side does the same
// (svn.ParseLayout); this one only labels the header chip.
function branchOf(rel) {
  const segs = rel.replace(/^\^?\/?/, "").split("/");
  for (let i = 0; i < segs.length; i++) {
    if (segs[i] === "trunk") return { kind: "trunk", name: "trunk" };
    if ((segs[i] === "branches" || segs[i] === "tags") && i + 1 < segs.length && segs[i + 1]) {
      return { kind: segs[i] === "tags" ? "tag" : "branch", name: decodeURIComponent(segs[i + 1]) };
    }
  }
  return null;
}

function showBranchChip(rel) {
  const b = branchOf(rel);
  const chip = $("wcBranch");
  chip.hidden = !b;
  if (!b) return;
  chip.textContent = b.kind === "trunk" ? "trunk" : `${b.kind}: ${b.name}`;
  chip.classList.toggle("tag", b.kind === "tag");
  chip.title = b.kind === "tag" ? "On a tag (a snapshot): commits here would change it. Click for branches." : "Branches: switch, or make a new one";
}
$("wcBranch").onclick = () => setTab("branches");

state.branches = null;

async function loadBranches() {
  const wc = state.view.path;
  $("branchesInfo").textContent = "Asking the server…";
  try {
    const b = await call("Branches", wc);
    if (!state.view || state.view.path !== wc) return;
    state.branches = b;
    renderBranches();
  } catch (e) {
    $("branchesInfo").textContent = "";
    report(e);
  }
}

function renderBranches() {
  const b = state.branches;
  const tb = $("branches");
  tb.replaceChildren();
  const layout = b.layout.kind !== "";
  $("newBranchBtn").disabled = $("newTagBtn").disabled = !layout;
  $("branchesHelp").hidden = layout;
  $("branchesHelp").textContent = layout ? "" :
    "This repository doesn't use the usual trunk / branches / tags folders, so TrunkLine can't list its branches. " +
    "You can still switch to any folder with Switch to URL.";
  const kinds = { trunk: "trunk", branch: "branch", tag: "tag" };
  for (const br of b.branches) {
    const actions = el("td", { class: "actions" });
    actions.append(el("button", { onclick: () => flow(() => openCompare({ url: br.url, rev: 0, label: branchLabel(br) })),
      title: `What differs between ${branchLabel(br)} and another branch, tag or revision` }, "Compare…"));
    if (br.current) {
      actions.append(el("span", { class: "chip info" }, "you are here"));
    } else {
      actions.append(el("button", { onclick: () => flow(() => switchTo(br)) }, "Switch"));
      actions.append(el("button", { onclick: () => flow(() => openMerge(br)), title: `Bring ${br.name}'s changes into your working copy` }, "Merge into mine…"));
      if (br.kind !== "trunk") actions.append(el("button", { class: "ghost danger", onclick: () => flow(() => deleteBranch(br)) }, "Delete"));
    }
    const c = br.commit;
    tb.append(el("tr", { class: br.current ? "current" : "" },
      el("td", { class: "kind" }, kinds[br.kind]),
      el("td", { class: br.kind === "tag" ? "warn-text" : "" }, br.name),
      el("td", { class: "muted" }, c.revision ? `r${c.revision} by ${c.author || "unknown"}, ${fmtDate(c.date)}` : ""),
      actions));
  }
  const n = k => b.branches.filter(x => x.kind === k).length;
  $("branchesInfo").textContent = layout
    ? `${n("branch")} branch${n("branch") === 1 ? "" : "es"} · ${n("tag")} tag${n("tag") === 1 ? "" : "s"}` +
      (b.layout.rest ? ` · you have ${decodeURIComponent(b.layout.rest)} checked out, so switches keep that folder` : "")
    : "";
}

// confirmSwitch warns about what a switch to target ({kind, name}) carries or means.
async function confirmSwitch(target, changes) {
  const notes = [];
  if (changes) notes.push(`Your ${changes} local change${changes === 1 ? "" : "s"} come${changes === 1 ? "s" : ""} along. ` +
    "Where they clash with what's on " + target.name + ", you'll get conflicts to resolve.");
  if (target.kind === "tag") notes.push(`${target.name} is a tag: a snapshot, usually of a release. Switch to it to look, build or test; ` +
    "if you commit while on it, you change the snapshot. Make a branch from it to work on it.");
  if (!notes.length) return true;
  return confirmDanger(`Switch to ${target.name}?`, notes.join("\n\n"), "Switch");
}

async function doSwitch(url, label) {
  const wc = state.view.path;
  let ok = false;
  try {
    await withProgress("Switching to " + label, () => call("SwitchTo", wc, url));
    ok = true;
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
  }
  state.log = [];
  await reloadLists();
  await refreshWc(false);
  checkIncoming();
  if (ok && !conflictsAfter("Switched to " + label)) {
    $("progDlg").close();
    toast("Switched to " + label + ".", "ok");
  }
  if (currentTab() === "branches") await loadBranches();
}

async function switchTo(br) {
  if (!await confirmSwitch(br, state.branches.changes)) return;
  await doSwitch(br.url, br.name);
}

async function deleteBranch(br) {
  const ok = await confirmDanger(`Delete ${br.kind} ${br.name}?`,
    `${br.name} disappears from the list, for everyone. It stays in the history (last changed in r${br.commit.revision}), ` +
    "so it can be brought back. Anyone with a working copy on it should switch away first.", "Delete " + br.kind);
  if (!ok) return;
  try {
    const rev = await call("DeleteBranch", state.view.path, br.kind, br.name, "");
    toast(`Deleted ${br.kind} ${br.name} (r${rev}).`, "ok");
  } catch (e) { report(e); }
  await loadBranches();
}

// newBranch asks for a new branch or tag's name and makes it; atRev, when
// given, copies the code as it was at that revision (from the History tab).
async function newBranch(kind, atRev) {
  const b = state.branches;
  const from = b.branches.find(x => x.current);
  const fromName = from ? from.name : b.layout.name;
  $("branchTitle").textContent = kind === "tag" ? "New tag" : "New branch";
  $("branchWhat").textContent = kind === "tag"
    ? `A snapshot of ${fromName}, e.g. for a release. Tags aren't meant to change after.`
    : `A copy of ${fromName} to work on separately. Making it is quick and keeps the history.`;
  $("branchFromHead").textContent = `the latest on the server`;
  $("branchFromWc").textContent = `my working copy's revision (r${b.revision})`;
  $("branchFromRevRow").hidden = !atRev;
  if (atRev) $("branchFromRev").textContent = `revision r${atRev} (picked in the history)`;
  document.querySelector(`input[name="branchFrom"][value="${atRev ? "rev" : "head"}"]`).checked = true;
  $("branchName").value = "";
  $("branchMsg").value = "";
  $("branchSwitch").checked = kind === "branch";
  $("branchOk").textContent = kind === "tag" ? "Create tag" : "Create branch";
  const note = () => {
    $("branchNote").textContent = $("branchSwitch").checked && b.changes
      ? `Your ${b.changes} local change${b.changes === 1 ? "" : "s"} will come along to the new ${kind} (not committed to it).` : "";
  };
  $("branchSwitch").onchange = note;
  note();
  const r = ask($("branchDlg"));
  $("branchName").focus();
  if (await r !== "ok") return;
  const name = $("branchName").value.trim();
  let fromWhere = document.querySelector('input[name="branchFrom"]:checked').value;
  if (fromWhere === "rev") fromWhere = "r" + atRev;
  let rev;
  try {
    rev = await call("CreateBranch", state.view.path, kind, name, fromWhere, $("branchMsg").value.trim());
  } catch (e) { return report(e); }
  toast(`Created ${kind} ${name} (r${rev}).`, "ok");
  if ($("branchSwitch").checked) {
    await loadBranches();
    const nb = state.branches.branches.find(x => x.kind === kind && x.name === name);
    if (nb) {
      if (kind === "tag" && !await confirmSwitch(nb, 0)) return;
      await doSwitch(nb.url, name);
      return;
    }
  }
  await loadBranches();
}

$("newBranchBtn").onclick = () => state.branches && newBranch("branch");
$("newTagBtn").onclick = () => state.branches && newBranch("tag");
$("relocateBtn").onclick = async () => {
  if (!state.info) return;
  $("relocateOld").value = state.info.repoRoot;
  $("relocateNew").value = state.info.repoRoot;
  const r = ask($("relocateDlg"));
  $("relocateNew").select();
  if (await r !== "ok") return;
  try {
    const url = await call("Relocate", state.view.path, $("relocateNew").value);
    await reloadLists();
    await openWc(state.view.path);
    toast("This working copy now uses " + url, "ok");
  } catch (e) { report(e); }
};

$("branchesRefreshBtn").onclick = () => busy($("branchesRefreshBtn"), loadBranches);
$("switchUrlBtn").onclick = async () => {
  $("switchUrl").value = state.branches ? state.branches.url : (state.info ? state.info.url : "");
  const r = ask($("urlDlg"));
  $("switchUrl").select();
  if (await r !== "ok") return;
  const url = $("switchUrl").value.trim();
  if (!url) return;
  const target = { kind: "other", name: url, ...(branchOf(url.replace(/^[a-z+]+:\/\/[^/]*/, "")) || {}) };
  if (!await confirmSwitch(target, state.branches ? state.branches.changes : 0)) return;
  await doSwitch(url, target.name);
};
