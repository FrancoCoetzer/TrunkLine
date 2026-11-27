"use strict";
// TrunkLine's page: the revision graph.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- The revision graph ---------------------------------------------------------------------------
//
// Lanes (trunk, branches) as columns, commits as rows (newest first): a
// line along each lane while it exists, a curve from where each branch was
// copied, a dashed curve for each merge, and tags as labels on their row.

const LANE_COLOURS = ["var(--cyan)", "var(--accent)", "var(--green)", "var(--red)", "var(--ink-dim)"];
const GRAPH_ROW = 26, GRAPH_COL = 18;

async function openGraph() {
  if (!state.view || state.view.type !== "wc") return;
  $("graphBody").replaceChildren(el("div", { class: "placeholder" }, "Reading the history…"));
  $("graphDetail").replaceChildren("Pick a commit.");
  $("graphStats").textContent = "";
  $("graphTitle").textContent = "Revision graph: " + $("wcName").textContent;
  if (!$("graphDlg").open) ask($("graphDlg"));
  let g;
  try { g = await call("RevisionGraph", state.view.path); } catch (e) {
    $("graphBody").replaceChildren(el("div", { class: "placeholder" }, parseErr(e).message));
    return;
  }
  renderGraph(g);
}
$("graphBtn").onclick = () => flow(openGraph);

function renderGraph(g) {
  const svgNS = "http://www.w3.org/2000/svg";
  const n = g.commits.length;
  $("graphStats").textContent = `${n} commit${n === 1 ? "" : "s"} · ${g.lanes.length} lane${g.lanes.length === 1 ? "" : "s"} · ${g.merges.length} merge${g.merges.length === 1 ? "" : "s"}`;
  if (!n) return $("graphBody").replaceChildren(el("div", { class: "placeholder" }, "No commits."));
  const colour = i => LANE_COLOURS[i % LANE_COLOURS.length];
  const x = lane => 12 + lane * GRAPH_COL;
  const y = row => row * GRAPH_ROW + GRAPH_ROW / 2;
  // The row of a revision: its commit, or the newest older one.
  const rowOf = rev => { const i = g.commits.findIndex(c => c.rev <= rev); return i < 0 ? n - 1 : i; };
  const rowsOfLane = g.lanes.map((_, i) => g.commits.map((c, r) => c.lane === i ? r : -1).filter(r => r >= 0));
  const width = 24 + g.lanes.length * GRAPH_COL;
  const svg = document.createElementNS(svgNS, "svg");
  svg.setAttribute("width", width);
  svg.setAttribute("height", n * GRAPH_ROW);
  svg.setAttribute("aria-hidden", "true");
  const add = (tag, attrs) => { const e = document.createElementNS(svgNS, tag); for (const k in attrs) e.setAttribute(k, attrs[k]); svg.append(e); return e; };
  const curve = (x1, y1, x2, y2) => `M${x1} ${y1} C${x1} ${(y1 + y2) / 2} ${x2} ${(y1 + y2) / 2} ${x2} ${y2}`;
  // Lanes: from the newest row (the top, if still alive) to the oldest.
  g.lanes.forEach((l, i) => {
    const rows = rowsOfLane[i];
    if (!rows.length && l.to) return;
    const top = l.to ? (rows.length ? rows[0] : rowOf(l.to)) : 0;
    const bottom = l.from && rows.length ? rows[rows.length - 1] : n - 1;
    add("line", { x1: x(i), y1: top === 0 && !l.to ? 0 : y(top), x2: x(i), y2: l.from ? y(bottom) : n * GRAPH_ROW, stroke: colour(i), "stroke-width": 2, opacity: 0.55 });
    if (l.parent >= 0 && l.from) {
      add("path", { d: curve(x(l.parent), y(rowOf(l.parentRev)), x(i), y(bottom)), fill: "none", stroke: colour(i), "stroke-width": 2, opacity: 0.55 });
    }
  });
  for (const m of g.merges) {
    add("path", { d: curve(x(m.fromLane), y(rowOf(m.fromRev)), x(m.toLane), y(rowOf(m.toRev))), fill: "none", stroke: colour(m.fromLane),
      "stroke-width": 1.6, "stroke-dasharray": "4 3" });
  }
  g.commits.forEach((c, r) => add("circle", { cx: x(c.lane), cy: y(r), r: 4.5, fill: colour(c.lane), stroke: "var(--panel)", "stroke-width": 1.5 }));

  const rows = el("div", { class: "rows" });
  g.commits.forEach((c, r) => {
    const l = g.lanes[c.lane];
    const row = el("div", { class: "row", tabindex: r === 0 ? 0 : -1, role: "button" },
      el("span", { class: "grev" }, "r" + c.rev),
      el("span", { class: "lane", style: `color:${colour(c.lane)}` }, l.name),
      ...(c.tags || []).map(t => el("span", { class: "tag", title: "tag " + t }, t)),
      el("span", { class: "msg", title: c.message }, c.message || "(no message)"),
      el("span", { class: "who" }, (c.author || "") + "  " + fmtDate(c.date)));
    row.onclick = () => pickGraphRow(g, r, rows);
    row.onkeydown = e => {
      const to = { ArrowDown: r + 1, ArrowUp: r - 1, Home: 0, End: n - 1 }[e.key];
      if (to !== undefined && to >= 0 && to < n) { e.preventDefault(); rows.children[to].focus(); pickGraphRow(g, to, rows); }
      if (e.key === "Enter" || e.key === " ") { e.preventDefault(); pickGraphRow(g, r, rows); }
    };
    rows.append(row);
  });
  const legend = el("div", { class: "legend" }, ...g.lanes.map((l, i) => el("span", { style: `--c:${colour(i)}` },
    l.name + (l.to ? " (deleted)" : ""))), el("span", { class: "muted", style: "--c:transparent" }, "- - - merge"));
  $("graphBody").replaceChildren(legend, el("div", { class: "graph" }, svg, rows));
}

function pickGraphRow(g, r, rows) {
  for (const x of rows.children) x.classList.remove("sel");
  rows.children[r].classList.add("sel");
  const c = g.commits[r], l = g.lanes[c.lane];
  const url = state.info.repoRoot + encodePath(l.path);
  const merges = g.merges.filter(m => m.toRev === c.rev).map(m => `merged ${g.lanes[m.fromLane].name} up to r${m.fromRev}`);
  $("graphDetail").replaceChildren(
    el("b", {}, "r" + c.rev), el("span", {}, `${l.name} · ${c.author || "unknown"} · ${fmtDate(c.date)}`),
    ...(merges.length ? [el("span", { class: "muted" }, merges.join(", "))] : []),
    el("span", { style: "flex:1" }),
    l.to ? el("span", { class: "muted" }, "(the branch was deleted in r" + l.to + ")")
      : el("button", { type: "button", onclick: () => { $("graphDlg").close(); flow(() => openCompare({ url, rev: c.rev, label: `r${c.rev} of ${l.name}` })); } }, "Compare…"));
}
