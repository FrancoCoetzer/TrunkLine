"use strict";
// TrunkLine's page: updates.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

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
    (info.canInstall ? "" : `  (${info.manual})`);
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
  if (info.available) return info.canInstall ? `${info.latest} is available` : `${info.latest} is available (${info.manual})`;
  if (info.note) return info.note + (info.latest ? ` (latest release: ${info.latest})` : "");
  return info.latest ? `Up to date (latest release: ${info.latest})` : "No releases published yet";
}

async function openAbout() {
  const s = state.startup;
  const kind = await Go().InstallKind();
  $("aboutVersion").textContent = `${s.version} (${{ deb: ".deb package", rpm: ".rpm package", flatpak: "Flatpak", snap: "Snap", windows: "Windows setup", msi: "Windows .msi", source: "built from source" }[kind] || kind})`;
  $("aboutSvn").textContent = s.svnError ? "not found" : `${s.svnVersion} (${s.svnPath})`;
  if (!$("aboutDlg").open) ask($("aboutDlg"));
}
$("version").onclick = openAbout;
$("prefsNotify").onchange = async () => {
  const on = $("prefsNotify").checked;
  try {
    await Go().SetNotify(on);
    state.startup.notify = on;
  } catch (e) { report(e); }
};
$("prefsAuto").onchange = async () => {
  const on = $("prefsAuto").checked;
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

// Buttons that open a dialog or start a long action: one at a time (flow).
for (const id of ["addWcBtn", "welcomeWc", "addServerBtn", "welcomeServer", "revertBtn", "ignoreBtn", "removeWcBtn",
  "removeServerBtn", "checkoutHereBtn", "exportHereBtn", "importHereBtn", "newBranchBtn", "newTagBtn", "relocateBtn",
  "switchUrlBtn", "lockBtn", "lockPickBtn", "unlockBtn", "updateInstall", "updateBtn", "savePatchBtn", "applyPatchBtn", "moveBtn", "moveFileBtn", "foldersBtn"]) {
  const handler = $(id).onclick;
  if (handler) $(id).onclick = e => flow(() => handler(e));
}
