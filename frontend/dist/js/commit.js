"use strict";
// TrunkLine's page: commit, revert, add, resolve; changelists.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Commit, revert, add, resolve ----------------------------------------------------------

// withMarkersCheck runs f(false); if Go refuses because conflict markers are
// still in a file, it asks, and runs f(true) only if the user insists.
async function withMarkersCheck(f, okLabel) {
  try {
    return await f(false);
  } catch (e) {
    const err = parseErr(e);
    if (err.kind !== "markers") throw err;
    const msg = err.message + "\n\nOnly continue if those lines are meant to be in the file.";
    if (!await confirmDanger("Conflict markers left in the file", msg, okLabel)) throw { kind: "canceled", message: "Canceled" };
    return await f(true);
  }
}

async function commit() {
  const sel = checkedChanges();
  const msg = $("commitMsg").value;
  if (!sel.length) return toast("Tick the changes to commit.");
  if (!msg.trim()) { $("commitMsg").focus(); return toast("Write a commit message first."); }
  await busy($("commitBtn"), async () => {
    const opts = { allowMarkers: false, keepLocks: !$("keepLocksBox").hidden && $("keepLocks").checked, allowPartialMerge: false };
    let rev;
    for (;;) {
      try {
        rev = await call("Commit", state.view.path, msg, sel.map(c => c.path), opts);
        break;
      } catch (e) {
        const err = parseErr(e);
        if (err.kind === "markers" && !opts.allowMarkers) {
          if (!await confirmDanger("Conflict markers left in the file", err.message + "\n\nOnly continue if those lines are meant to be in the file.", "Commit anyway")) throw { kind: "canceled", message: "Canceled" };
          opts.allowMarkers = true;
        } else if (err.kind === "partialmerge" && !opts.allowPartialMerge) {
          if (!await confirmDanger("Commit only part of a merge?", err.message, "Commit only these")) throw { kind: "canceled", message: "Canceled" };
          opts.allowPartialMerge = true;
        } else {
          throw err;
        }
      }
    }
    $("keepLocks").checked = false;
    $("commitMsg").value = "";
    state.log = [];
    toast(rev ? `Committed revision ${rev}.` : "Committed.", "ok");
    clearBanner();
    await refreshWc(false);
    $("commitMsg").focus(); // the Commit button is disabled now, which would drop focus to the page
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

$("ignoreBtn").onclick = async () => {
  const sel = checkedChanges().filter(c => c.item === "unversioned");
  if (!sel.length) return;
  const names = sel.map(c => c.path.split("/").pop());
  const exts = [...new Set(names.map(n => (n.lastIndexOf(".") > 0 ? n.slice(n.lastIndexOf(".")) : "")))];
  const oneExt = exts.length === 1 && exts[0] && !sel.some(c => c.isDir) ? exts[0] : "";
  $("ignoreWhat").textContent = sel.slice(0, 10).map(c => "  " + c.path + (c.isDir ? "/" : "")).join("\n") +
    (sel.length > 10 ? `\n  … and ${sel.length - 10} more` : "");
  $("ignoreByName").textContent = sel.length === 1 ? `Just ${names[0]}` : "Just these names";
  $("ignoreByExtRow").hidden = !oneExt;
  $("ignoreByExt").textContent = `Every ${oneExt} file`;
  document.querySelector('input[name="ignoreBy"][value="' + (oneExt ? "ext" : "name") + '"]').checked = true;
  $("ignoreRecursive").checked = false;
  if (await ask($("ignoreDlg")) !== "ok") return;
  const byExt = document.querySelector('input[name="ignoreBy"]:checked').value === "ext";
  await busy($("ignoreBtn"), async () => {
    const folders = await call("Ignore", state.view.path, sel.map(c => c.path), byExt, $("ignoreRecursive").checked);
    for (const c of sel) state.checked.delete(c.path);
    // Tick the folders whose ignore list changed, ready to commit.
    for (const f of folders) { state.checked.add(f); state.known.add(f); }
    await refreshWc(false);
    const names = folders.map(f => f === "." ? "the top folder" : f + "/").join(", ");
    if (folders.length) setBanner("info", `Ignored. The ignore list of ${names} changed: it's ticked, so commit it and everyone gets the same ignores.`);
    else toast("Those were already ignored.", "ok");
  });
};

$("resolveBtn").onclick = () => busy($("resolveBtn"), async () => {
  const sel = checkedChanges().filter(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict);
  if (!sel.length) return toast("Tick the conflicted files to resolve.");
  const how = $("resolveHow").value;
  if (!how) { $("resolveHow").focus(); return toast("Choose how to resolve first: keep mine, take theirs, or mark your edited file resolved."); }
  await withMarkersCheck(allow => call("Resolve", state.view.path, how, sel.map(c => c.path), allow), "Mark resolved anyway");
  $("resolveHow").value = "";
  toast(`Resolved ${sel.length} conflict${sel.length === 1 ? "" : "s"}.`, "ok");
  await refreshWc(false);
});

// ---- Changelists -------------------------------------------------------------------------------
//
// Named groups of changed files, to commit separately. svn keeps them in this
// working copy only; a commit takes the committed files out of theirs.

// canChangelist: only files svn knows about can be in a changelist.
function canChangelist(c) {
  return !c.isDir && !["unversioned", "ignored", "external"].includes(c.item);
}

async function setChangelist(files, name) {
  try {
    await call("SetChangelist", state.view.path, files.map(c => c.path), name);
  } catch (e) { return report(e); }
  const n = files.length === 1 ? files[0].path : files.length + " files";
  const from = [...new Set(files.map(c => c.changelist))];
  toast(name ? `Put ${n} in changelist ${name}.` : `Took ${n} out of ${from.length === 1 ? "changelist " + from[0] : "their changelists"}.`, "ok");
  await refreshWc(false);
}

$("changelistBtn").onclick = async () => {
  const sel = checkedChanges().filter(canChangelist);
  if (!sel.length) return toast("Tick files that are in svn (not new ones or folders) to group them.");
  const skipped = checkedChanges().length - sel.length;
  $("clFiles").textContent = sel.slice(0, 10).map(c => "  " + c.path + (c.changelist ? `  (in ${c.changelist})` : "")).join("\n") +
    (sel.length > 10 ? `\n  … and ${sel.length - 10} more` : "") +
    (skipped ? `\n\n${skipped} ticked item${skipped === 1 ? " is" : "s are"} new or a folder and can't be in a changelist.` : "");
  const names = [...new Set(state.changes.map(c => c.changelist).filter(Boolean))].sort();
  $("clNames").replaceChildren(...names.map(n => el("option", { value: n })));
  const common = [...new Set(sel.map(c => c.changelist))];
  $("clName").value = common.length === 1 && common[0] ? common[0] : "";
  $("clRemove").hidden = !sel.some(c => c.changelist);
  for (;;) {
    const r = ask($("clDlg"));
    $("clName").select();
    const how = await r;
    if (how === "remove") return setChangelist(sel.filter(c => c.changelist), "");
    if (how !== "ok") return;
    const name = $("clName").value.trim();
    if (name) return setChangelist(sel, name);
    toast("Give the changelist a name.");
  }
};
