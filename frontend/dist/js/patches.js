"use strict";
// TrunkLine's page: patches.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Patches -----------------------------------------------------------------------------------
//
// Save ticked changes to a .patch file for someone else, or apply one to
// this working copy (svn patch). The patch's paths start at the working
// copy, so it applies to any working copy of the same folder.

function showErrIn(p, e) {
  p.textContent = e ? parseErr(e).message : "";
  p.hidden = !e;
}

function defaultPatchFile() {
  const name = (state.view.path.split(/[\\/]/).filter(Boolean).pop() || "changes").replace(/[^\w.-]+/g, "-");
  return (state.startup.home || "") + sep() + name + "-changes.patch";
}

$("savePatchBtn").onclick = async () => {
  const sel = checkedChanges();
  if (!sel.length) return toast("Tick the changes to put in the patch first.");
  const fresh = sel.filter(c => c.item === "unversioned");
  $("patchFiles").textContent = sel.slice(0, 10).map(c => "  " + c.path + (c.item === "unversioned" ? "  (new: Add it first to include it)" : "")).join("\n") +
    (sel.length > 10 ? `\n  … and ${sel.length - 10} more` : "") +
    (fresh.length ? `\n\nNew files go in a patch once they're added (Add).` : "");
  if (!$("patchFile").value) $("patchFile").value = defaultPatchFile();
  showErrIn($("patchSaveErr"), null);
  for (;;) {
    const r = ask($("patchSaveDlg"));
    $("patchFile").focus();
    if (await r !== "ok") return;
    const file = $("patchFile").value.trim(), paths = sel.map(c => c.path);
    let res;
    try {
      try {
        res = await call("SavePatch", state.view.path, paths, file, false);
      } catch (e) {
        if (parseErr(e).kind !== "exists") throw e;
        if (!await confirmDanger("Replace the file?", `${file} already exists. Replace it with this patch?`, "Replace")) continue;
        res = await call("SavePatch", state.view.path, paths, file, true);
      }
    } catch (e) { showErrIn($("patchSaveErr"), e); continue; }
    const n = res.files === 1 ? "1 file" : res.files + " files";
    toast(`Saved ${n} to ${res.file}.` + (res.skipped.length ? ` Left out (new, not added yet): ${res.skipped.join(", ")}.` : ""), "ok");
    return;
  }
};
$("patchChoose").onclick = async () => {
  try {
    const p = await call("PickSaveFile", "Save the patch as", $("patchFile").value.split(/[\\/]/).pop() || "changes.patch");
    if (p) $("patchFile").value = p;
  } catch (e) { showErrIn($("patchSaveErr"), e); }
};

const PATCH_WHAT = { added: "new file", deleted: "delete", updated: "change", merged: "already in", conflict: "won't fit", skipped: "skipped" };

// showPatchResult lists what svn patch did, or with dry would do.
function showPatchResult(res, dry) {
  const box = $("patchPreview");
  box.replaceChildren();
  for (const p of res) {
    const what = PATCH_WHAT[p.action] || (p.props ? "properties" : "");
    const bad = p.rejected || p.action === "skipped";
    box.append(el("div", { class: "p" + (bad ? " bad" : "") }, el("span", { class: "what" }, what), el("span", {}, p.path)));
    if (p.action === "skipped") box.append(el("div", { class: "note" }, "not in this working copy: was the patch made from another folder?"));
    for (const n of p.notes || []) if (/rejected|fuzz/.test(n)) box.append(el("div", { class: "note" }, n));
  }
  const rejected = res.filter(p => p.rejected).length, skipped = res.filter(p => p.action === "skipped").length;
  const ok = res.length - rejected - skipped;
  box.append(el("div", { class: "sum" }, (dry ? `${ok} of ${res.length} would go in cleanly.` : `${ok} of ${res.length} went in cleanly.`) +
    (rejected ? ` ${rejected} won't fit: those parts go in .svnpatch.rej files.` : "") +
    (skipped ? ` ${skipped} skipped.` : "")));
}

async function checkPatch() {
  const file = $("patchIn").value.trim();
  if (!file) return;
  showErrIn($("patchApplyErr"), null);
  $("patchPreview").replaceChildren(el("div", { class: "muted" }, "Checking…"));
  try {
    showPatchResult(await call("ApplyPatch", state.view.path, file, true), true);
  } catch (e) {
    $("patchPreview").replaceChildren();
    showErrIn($("patchApplyErr"), e);
  }
}
$("patchCheck").onclick = checkPatch;
$("patchIn").addEventListener("keydown", e => { if (e.key === "Enter") { e.preventDefault(); checkPatch(); } });
// Not on "change": that fires when focus leaves for the Apply button, and the
// preview redrawing then moves the button from under the pointer.
$("patchInChoose").onclick = async () => {
  try {
    const p = await call("PickPatchFile", "Choose a patch to apply");
    if (p) { $("patchIn").value = p; checkPatch(); }
  } catch (e) { showErrIn($("patchApplyErr"), e); }
};

$("applyPatchBtn").onclick = async () => {
  $("patchPreview").replaceChildren();
  showErrIn($("patchApplyErr"), null);
  if ($("patchIn").value) checkPatch();
  for (;;) {
    const r = ask($("patchApplyDlg"));
    $("patchIn").focus();
    if (await r !== "ok") return;
    let res;
    try {
      res = await call("ApplyPatch", state.view.path, $("patchIn").value.trim(), false);
    } catch (e) { showErrIn($("patchApplyErr"), e); continue; }
    await refreshWc(false);
    const bad = res.filter(p => p.rejected), skipped = res.filter(p => p.action === "skipped");
    if (bad.length || skipped.length) {
      setBanner("conflict", "The patch only partly went in." +
        (bad.length ? ` Some changes to ${bad.map(p => p.path).join(", ")} didn't fit the file as it is: they're saved next to it as ${bad.length === 1 ? "a .svnpatch.rej file" : ".svnpatch.rej files"}. Make those changes by hand, then delete the .rej ${bad.length === 1 ? "file" : "files"}.` : "") +
        (skipped.length ? ` Skipped (not in this working copy): ${skipped.map(p => p.path).join(", ")}.` : ""));
    } else {
      toast(`Applied the patch: ${res.length === 1 ? "1 file" : res.length + " files"} changed. Review them, then commit.`, "ok");
    }
    return;
  }
};
