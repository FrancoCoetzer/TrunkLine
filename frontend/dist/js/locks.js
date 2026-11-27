"use strict";
// TrunkLine's page: locks.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Locks -------------------------------------------------------------------------------------

const LOCK_TEXT = { mine: "locked by you", other: "locked", stolen: "your lock was taken over", broken: "your lock was removed" };

// lockMark is the small "locked by …" note after a file name, or null.
function lockMark(lockState, lock) {
  if (!lockState) return null;
  const who = lock && lock.owner ? lock.owner : "";
  const text = lockState === "other" ? `locked by ${who || "someone"}` : LOCK_TEXT[lockState];
  const tip = lock ? `${text}${lock.comment ? ": " + lock.comment : ""}${lock.created ? " (" + fmtDate(lock.created) + ")" : ""}` : text;
  return el("span", { class: "lockmark lock-" + lockState, title: tip }, "[" + text + "]");
}

state.locks = [];
state.lockChecked = new Set();

async function loadLocks() {
  const wc = state.view.path;
  $("locksCount").textContent = "Asking the server…";
  try {
    const files = await call("Locks", wc);
    if (!state.view || state.view.path !== wc) return;
    state.locks = files;
    const paths = new Set(files.map(f => f.path));
    for (const p of state.lockChecked) if (!paths.has(p)) state.lockChecked.delete(p);
    renderLocks();
  } catch (e) {
    $("locksCount").textContent = "";
    report(e);
  }
}

function renderLocks() {
  const tb = $("locks");
  const hadFocus = tb.contains(document.activeElement);
  tb.replaceChildren();
  for (const f of state.locks) {
    const box = el("input", { type: "checkbox", "aria-label": "Select " + f.path });
    box.checked = state.lockChecked.has(f.path);
    box.onchange = () => { if (box.checked) state.lockChecked.add(f.path); else state.lockChecked.delete(f.path); updateLockButtons(); };
    let lockCell;
    if (f.state === "other") lockCell = el("span", { class: "lock-other" }, "locked by " + (f.lock.owner || "someone"));
    else if (f.state) lockCell = el("span", { class: "lock-" + f.state }, LOCK_TEXT[f.state] +
      (f.state === "stolen" && f.lock ? " by " + f.lock.owner : "") + (f.state !== "mine" ? " (Update clears it)" : ""));
    else lockCell = el("span", { class: "muted" }, f.needsLock ? "needs lock: read-only until you lock it" : "not locked");
    tb.append(lockRow(f, box,
      el("td", {}, box),
      el("td", { class: "path", title: f.path }, f.path, f.modified ? el("span", { class: "lockmark st-modified" }, "[modified]") : null),
      el("td", {}, lockCell),
      el("td", { class: "msg", title: f.lock ? f.lock.comment : "" }, f.lock ? f.lock.comment : ""),
      el("td", { class: "muted" }, f.lock ? fmtDate(f.lock.created) : "")));
  }
  rovingRows(tb, hadFocus);
  const mine = state.locks.filter(f => f.state === "mine").length;
  const others = state.locks.filter(f => f.state === "other").length;
  $("locksCount").textContent = state.locks.length
    ? `${mine} locked by you · ${others} locked by others · ${state.locks.filter(f => f.needsLock).length} need a lock`
    : "No locked files, and none that need a lock";
  $("locksHelp").hidden = state.locks.length > 0;
  updateLockButtons();
}

function lockSelection() { return state.locks.filter(f => state.lockChecked.has(f.path)); }

function updateLockButtons() {
  const sel = lockSelection();
  $("lockBtn").disabled = !sel.some(f => f.state !== "mine");
  $("unlockBtn").disabled = !sel.some(f => f.state === "mine" || f.state === "other");
}

// askLockAndLock asks for the note, locks paths, and offers to take over
// any that someone else holds.
async function askLockAndLock(paths) {
  $("lockTitle").textContent = paths.length === 1 ? "Lock " + paths[0] : `Lock ${paths.length} files`;
  $("lockFiles").textContent = "Only you will be able to commit " + (paths.length === 1 ? "it" : "them") +
    " until you commit or unlock. Others see your name and this note.";
  $("lockComment").value = "";
  const r = ask($("lockDlg"));
  $("lockComment").focus();
  if (await r !== "ok") return;
  const comment = $("lockComment").value.trim();
  const wc = state.view.path;
  try {
    await call("LockFiles", wc, paths, comment, false);
    toast(paths.length === 1 ? `Locked ${paths[0]}.` : `Locked ${paths.length} files.`, "ok");
  } catch (e) {
    const err = parseErr(e);
    if (err.kind !== "lockconflict") return report(err);
    const ok = await confirmDanger("Take over the lock?", err.message +
      "\n\nTaking it over means they can no longer commit their changes to it, and may lose that work. " +
      "Ask them first if you can.", "Take over the lock");
    if (!ok) { toast("The files nobody else had locked are locked; the others are unchanged."); }
    else {
      try {
        await call("LockFiles", wc, paths, comment, true);
        toast("Lock taken over.", "ok");
      } catch (e2) { report(e2); }
    }
  }
  state.lockChecked.clear();
  if (currentTab() === "locks") await loadLocks();
  await refreshWc(state.incoming);
}

$("lockBtn").onclick = () => {
  const paths = lockSelection().filter(f => f.state !== "mine").map(f => f.path);
  if (paths.length) askLockAndLock(paths);
};
$("lockPickBtn").onclick = async () => {
  try {
    const rel = await call("PickFileIn", state.view.path, "Lock a file");
    if (rel) askLockAndLock([rel]);
  } catch (e) { report(e); }
};
$("locksRefreshBtn").onclick = () => busy($("locksRefreshBtn"), loadLocks);

$("unlockBtn").onclick = () => busy($("unlockBtn"), async () => {
  const sel = lockSelection();
  const mine = sel.filter(f => f.state === "mine").map(f => f.path);
  const theirs = sel.filter(f => f.state === "other");
  const wc = state.view.path;
  if (mine.length) {
    await call("UnlockFiles", wc, mine, false);
    toast(`Released ${mine.length} lock${mine.length === 1 ? "" : "s"}.`, "ok");
  }
  if (theirs.length) {
    const list = theirs.map(f => `  ${f.path} (locked by ${f.lock.owner}${f.lock.comment ? ": " + f.lock.comment : ""})`).join("\n");
    if (await confirmDanger("Break someone else's lock?", `These are locked by someone else:\n\n${list}\n\n` +
        "Breaking a lock means they can no longer commit their changes to the file. Only do this if they're " +
        "away or forgot to unlock, and ideally tell them.", "Break the lock")) {
      await call("UnlockFiles", wc, theirs.map(f => f.path), true);
      toast("Lock broken.", "ok");
    }
  }
  state.lockChecked.clear();
  await loadLocks();
  await refreshWc(state.incoming);
});
