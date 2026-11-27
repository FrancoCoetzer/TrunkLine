"use strict";
// TrunkLine's page: diffs; side-by-side diffs.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Diffs -------------------------------------------------------------------------------

const MAX_DIFF_LINES = 20000;

function renderDiff(target, text) {
  target._diffText = text; // to draw it again when switching side by side on or off
  target.replaceChildren();
  if (!text || !text.trim()) {
    target.append(el("div", { class: "placeholder" }, "No text changes to show."));
    return;
  }
  const lines = text.split(/\r?\n/); // files with Windows line endings: no stray \r on each line
  if (sideBySide() && lines.some(l => l.startsWith("@@ "))) return renderSideBySide(target, lines);
  const frag = document.createDocumentFragment();
  for (const l of lines.slice(0, MAX_DIFF_LINES)) {
    let cls = "";
    if (l.startsWith("+++") || l.startsWith("---") || l.startsWith("Index:") || l.startsWith("====") ||
        l.startsWith("Property changes on") || l.startsWith("Cannot display")) cls = "meta";
    else if (l.startsWith("@@")) cls = "hunk";
    else if (l.startsWith("+")) cls = "add";
    else if (l.startsWith("-")) cls = "del";
    frag.append(el("div", { class: cls }, l));
  }
  if (lines.length > MAX_DIFF_LINES) frag.append(el("div", { class: "meta" }, `… ${lines.length - MAX_DIFF_LINES} more lines not shown`));
  target.append(frag);
}

// ---- Side-by-side diffs ------------------------------------------------------------------------
//
// The same unified diff, drawn as old (left) and new (right) with line
// numbers: removed and added lines in a run are paired up row by row.
// Headers and property changes stay full width.

function sideBySide() { return store("sbs") === "1"; }

function setSideBySide(on) {
  store("sbs", on ? "1" : "0");
  for (const b of document.querySelectorAll(".sbsBtn")) b.setAttribute("aria-pressed", String(on));
  // Redraw the diffs on show (not one still loading, or cleared).
  for (const d of document.querySelectorAll(".diff")) {
    if (d._diffText !== undefined && d.childElementCount && !d.querySelector(":scope > .placeholder")) renderDiff(d, d._diffText);
  }
}
for (const b of document.querySelectorAll(".sbsBtn")) {
  b.setAttribute("aria-pressed", String(sideBySide()));
  b.onclick = () => setSideBySide(!sideBySide());
}

function renderSideBySide(target, lines) {
  const body = el("tbody");
  let inHunk = false, inProps = false, o = 0, n = 0, dels = [], adds = [];
  const full = (cls, text) => body.append(el("tr", { class: "full " + cls }, el("td", { colspan: 4 }, text)));
  const row = (left, right) => body.append(el("tr", {},
    el("td", { class: "n" }, left ? String(o++) : ""),
    el("td", { class: "code " + (left ? left.cls : "gap") }, left ? left.text : ""),
    el("td", { class: "n" }, right ? String(n++) : ""),
    el("td", { class: "code " + (right ? right.cls : "gap") }, right ? right.text : "")));
  const flush = () => {
    for (let i = 0; i < Math.max(dels.length, adds.length); i++) {
      row(i < dels.length ? { text: dels[i], cls: "del" } : null, i < adds.length ? { text: adds[i], cls: "add" } : null);
    }
    dels = [];
    adds = [];
  };
  for (const l of lines.slice(0, MAX_DIFF_LINES)) {
    const m = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(l);
    if (m) {
      flush();
      o = Number(m[1]);
      n = Number(m[2]);
      inHunk = true;
      inProps = false;
      full("hunk", l);
    } else if (/^(Index: |diff --git |Property changes on|====|___|Cannot display)/.test(l)) {
      flush();
      inHunk = false;
      inProps = l.startsWith("Property changes on");
      full("meta", l);
    } else if (!inHunk || inProps) {
      flush();
      full(inProps && l.startsWith("+") ? "padd" : inProps && l.startsWith("-") ? "pdel" : "meta", l);
    } else if (l.startsWith("-")) {
      dels.push(l.slice(1));
    } else if (l.startsWith("+")) {
      adds.push(l.slice(1));
    } else if (l.startsWith("\\")) {
      flush();
      full("meta", l); // \ No newline at end of file
    } else if (l !== "") {
      flush();
      const t = l.slice(1);
      row({ text: t, cls: "" }, { text: t, cls: "" });
    }
  }
  flush();
  if (lines.length > MAX_DIFF_LINES) full("meta", `… ${lines.length - MAX_DIFF_LINES} more lines not shown`);
  target.append(el("table", { class: "sbs" },
    el("colgroup", {}, el("col", { class: "n" }), el("col"), el("col", { class: "n" }), el("col")), body));
}

async function showDiff(ch) {
  state.diffPath = ch.path;
  for (const tr of $("changes").children) tr.classList.toggle("sel", tr.dataset.key === ch.path);
  $("diffTitle").textContent = ch.path;
  $("openFileBtn").hidden = false;
  // only files that are in the repository have a history to blame
  $("blameFileBtn").hidden = ch.isDir || ["unversioned", "added", "replaced", "missing"].includes(ch.item);
  $("historyFileBtn").hidden = ["unversioned", "added", "replaced"].includes(ch.item);
  $("propsFileBtn").hidden = ["unversioned", "missing", "deleted", "ignored", "obstructed"].includes(ch.item);
  $("mergeFileBtn").hidden = ch.item !== "conflicted" || ch.isDir;
  $("moveFileBtn").hidden = ["missing", "deleted", "ignored", "obstructed"].includes(ch.item) || ch.path === ".";
  const path = state.view.path;
  $("diff").replaceChildren(el("div", { class: "placeholder" }, "Loading…"));
  try {
    const d = await call("Diff", path, ch.path);
    if (state.diffPath === ch.path && state.view.path === path) renderDiff($("diff"), d);
  } catch (e) {
    $("diff").replaceChildren(el("div", { class: "placeholder" }, parseErr(e).message));
  }
}
$("openFileBtn").onclick = () => state.diffPath && Go().Open(state.view.path, state.diffPath);
