"use strict";
// TrunkLine's page: blame.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Blame -------------------------------------------------------------------------------------

const MAX_BLAME_ROWS = 30000;
let blame = null; // the result on show

// openBlame shows the blame view for what load returns (BlameFile/BlameURL);
// note, if given, is shown above the lines.
let blameNote = "";
let blameRel = null; // the working copy file being blamed, for its History button
async function openBlame(load, note = "", rel = null) {
  blameNote = note;
  blameRel = rel;
  $("blameHistoryBtn").hidden = !rel;
  const dlg = $("blameDlg");
  $("blameTitle").textContent = "Blame";
  $("blameStats").textContent = "";
  $("blameCommit").textContent = "Loading…";
  $("blameBody").replaceChildren();
  if (!dlg.open) dlg.showModal();
  try {
    blame = await load();
  } catch (e) {
    $("blameCommit").textContent = parseErr(e).message;
    return;
  }
  renderBlame();
}

function renderBlame() {
  const lines = blame.lines;
  $("blameTitle").textContent = "Blame: " + readable(blame.title);
  const revs = new Set(lines.map(l => l.revision));
  $("blameStats").textContent = `${lines.length} lines · ${revs.size} revision${revs.size === 1 ? "" : "s"}`;
  $("blameCommit").textContent = (blameNote ? blameNote + "\n" : "") +
    "Click a revision on the left to see its commit message. Brighter bars are newer changes.";
  if (!lines.length) {
    $("blameBody").replaceChildren(el("div", { class: "placeholder" }, "The file is empty."));
    return;
  }
  // Age: the bar's strength runs from the oldest revision in the file to the newest.
  const nums = [...revs].filter(r => r > 0);
  const lo = Math.min(...nums), hi = Math.max(...nums);
  const age = r => {
    const t = hi > lo ? (r - lo) / (hi - lo) : 1;
    return `color-mix(in oklab, var(--cyan) ${Math.round(15 + 85 * t)}%, transparent)`;
  };

  const tbody = el("tbody");
  let group = 0, prev = null;
  for (const l of lines.slice(0, MAX_BLAME_ROWS)) {
    const first = l.revision !== prev;
    if (first) group ^= 1;
    prev = l.revision;
    const meta = el("td", { class: "meta", title: l.revision ? `r${l.revision} by ${l.author || "?"}, ${fmtDate(l.date)}` : "" });
    meta.style.setProperty("--age", l.revision ? age(l.revision) : "var(--accent)");
    if (first && l.revision) {
      meta.append(el("span", { class: "brev" }, "r" + l.revision), " ", l.author || "", "  ", fmtDate(l.date).slice(0, 10));
    } else if (first) {
      meta.append(el("span", { class: "warn-text" }, "not committed"));
      meta.title = "Your local change, not committed yet";
    }
    if (l.revision) meta.onclick = () => showBlameRevision(l.revision);
    const tr = el("tr", { class: group ? "g1" : "", "data-rev": l.revision },
      meta, el("td", { class: "ln" }, String(l.line)), el("td", { class: "code" }, l.text));
    tbody.append(tr);
  }
  const table = el("table", { class: "blame" }, tbody);
  const body = [table];
  if (lines.length > MAX_BLAME_ROWS) body.push(el("div", { class: "placeholder" }, `… ${lines.length - MAX_BLAME_ROWS} more lines not shown`));
  $("blameBody").replaceChildren(...body);
}

async function showBlameRevision(rev) {
  for (const tr of $("blameBody").querySelectorAll("tr")) tr.classList.toggle("hl", tr.dataset.rev === String(rev));
  $("blameCommit").textContent = `r${rev}: loading…`;
  try {
    const [e] = await call("Log", blame.repoRoot, rev, 1, "");
    if (!e) throw { message: `r${rev} not found` };
    $("blameCommit").replaceChildren(
      el("b", {}, `r${e.revision}`), `  ${e.author || ""}  ${fmtDate(e.date)}  ·  ${e.paths.length} path${e.paths.length === 1 ? "" : "s"} changed\n`,
      e.message || "(no message)");
  } catch (err) {
    $("blameCommit").textContent = `r${rev}: ` + parseErr(err).message;
  }
}

$("blameHistoryBtn").onclick = () => {
  if (!blameRel) return;
  $("blameDlg").close();
  showFileHistory(blameRel);
};

$("historyFileBtn").onclick = () => {
  if (state.view && state.view.type === "wc" && state.diffPath && state.info) showFileHistory(state.diffPath);
};

$("blameFileBtn").onclick = () => {
  if (state.view && state.view.type === "wc" && state.diffPath) {
    const ch = state.changes.find(c => c.path === state.diffPath);
    openBlame(() => call("BlameFile", state.view.path, state.diffPath),
      ch && localChange(ch) ? "Lines marked \"not committed\" are your local changes." : "", state.diffPath);
  }
};
