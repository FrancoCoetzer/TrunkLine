"use strict";
// TrunkLine's page: the merge editor.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- The merge editor -----------------------------------------------------------------------------
//
// A conflicted text file as svn marked it: plain text, and conflicts with my
// lines and theirs (and the original). Each conflict is decided (mine,
// theirs, both either way, or written by hand), then the file is saved and
// marked resolved.

const ME_CHOICES = [["mine", "Mine"], ["theirs", "Theirs"], ["mine+theirs", "Mine, then theirs"], ["theirs+mine", "Theirs, then mine"], ["edit", "Edit…"]];
let me = null; // {rel, doc, picks: Map(chunk index -> {choice, text})}

async function openMergeEditor(rel) {
  let doc;
  try { doc = await call("MergeConflict", state.view.path, rel); } catch (e) { return report(e); }
  me = { rel, doc, picks: new Map() };
  $("meTitle").textContent = "Merge " + rel;
  $("meBase").checked = false;
  showErrIn($("meErr"), null);
  renderMerge();
  for (;;) {
    const r = ask($("mergeEdDlg"));
    nextConflict();
    if (await r !== "ok") { me = null; return; }
    const left = conflictsLeft();
    if (left) { showErrIn($("meErr"), { message: `Decide each conflict first (${left} left).` }); continue; }
    try {
      await call("SaveMerge", state.view.path, rel, mergedText(), me.doc.eol);
    } catch (e) { showErrIn($("meErr"), e); continue; }
    me = null;
    toast(`Merged ${rel} and marked it resolved. Check its diff, then commit.`, "ok");
    await refreshWc(false);
    const c = state.changes.find(c => c.path === rel);
    if (c) showDiff(c);
    return;
  }
}
$("mergeFileBtn").onclick = () => state.diffPath && flow(() => openMergeEditor(state.diffPath));

function linesOf(chunk, choice) {
  switch (choice) {
    case "mine": return chunk.mine || [];
    case "theirs": return chunk.theirs || [];
    case "mine+theirs": return [...(chunk.mine || []), ...(chunk.theirs || [])];
    case "theirs+mine": return [...(chunk.theirs || []), ...(chunk.mine || [])];
  }
  return [];
}

function conflictsLeft() {
  return me.doc.chunks.filter((c, i) => c.conflict && !me.picks.has(i)).length;
}

function mergedText() {
  const out = [];
  me.doc.chunks.forEach((c, i) => {
    if (!c.conflict) return out.push(...c.text);
    const p = me.picks.get(i);
    if (p.choice === "edit") { if (p.text !== "") out.push(...p.text.replace(/\r\n/g, "\n").replace(/\n$/, "").split("\n")); }
    else out.push(...linesOf(c, p.choice));
  });
  return out.join("\n") + (me.doc.final && out.length ? "\n" : "");
}

function renderMerge() {
  const body = $("meBody");
  const scroll = body.scrollTop;
  body.replaceChildren();
  const showBase = $("meBase").checked;
  me.doc.chunks.forEach((c, i) => {
    if (!c.conflict) {
      const lines = c.text;
      const box = el("div", { class: "me-text" });
      if (lines.length > 8 && !me.picks.has("open" + i)) {
        box.append(el("pre", {}, lines.slice(0, 3).join("\n")),
          el("button", { type: "button", class: "ghost", onclick: () => { me.picks.set("open" + i, true); renderMerge(); } }, `… ${lines.length - 6} unchanged lines`),
          el("pre", {}, lines.slice(-3).join("\n")));
      } else {
        box.append(el("pre", {}, lines.join("\n")));
      }
      return body.append(box);
    }
    const pick = me.picks.get(i);
    const side = (cls, label, lines) => el("div", { class: "me-side " + cls + (pick && (pick.choice === cls || pick.choice.includes(cls) && pick.choice !== "edit") ? " picked" : "") },
      el("div", { class: "lbl" }, label), el("pre", {}, lines.length ? lines.join("\n") : "(nothing)"));
    const sides = el("div", { class: "me-sides" }, side("mine", "Mine" + (c.mineLabel ? ` (${c.mineLabel})` : ""), c.mine || []));
    if (showBase) sides.append(side("base", "The original" + (c.base ? "" : " (not recorded)"), c.base || []));
    sides.append(side("theirs", "Theirs" + (c.theirsLabel ? ` (${c.theirsLabel})` : ""), c.theirs || []));
    sides.style.setProperty("--cols", showBase ? 3 : 2);
    const choices = el("div", { class: "me-choices", role: "group", "aria-label": "Use for this conflict" });
    for (const [v, label] of ME_CHOICES) {
      const b = el("button", { type: "button", "aria-pressed": String(!!pick && pick.choice === v) }, label);
      b.onclick = () => {
        me.picks.set(i, { choice: v, text: v === "edit" ? (pick ? (pick.choice === "edit" ? pick.text : linesOf(c, pick.choice).join("\n")) : (c.mine || []).join("\n")) : "" });
        renderMerge();
        if (v === "edit") { const ta = $("meBody").querySelector(`[data-chunk="${i}"] textarea`); if (ta) ta.focus(); }
      };
      choices.append(b);
    }
    const box = el("div", { class: "me-conflict" + (pick ? " decided" : "") }, sides, choices);
    box.dataset.chunk = i;
    if (pick && pick.choice === "edit") {
      const ta = el("textarea", { "aria-label": "Your text for this conflict", spellcheck: "false" });
      ta.value = pick.text;
      ta.oninput = () => { pick.text = ta.value; };
      box.append(ta);
    }
    body.append(box);
  });
  body.scrollTop = scroll;
  const total = me.doc.chunks.filter(c => c.conflict).length, left = conflictsLeft();
  $("meStats").textContent = left ? `${total - left} of ${total} conflicts decided` : `All ${total} decided`;
  $("meSave").disabled = left > 0;
}
$("meBase").onchange = () => me && renderMerge();

// nextConflict scrolls to the first conflict not decided yet.
function nextConflict() {
  if (!me) return;
  const i = me.doc.chunks.findIndex((c, j) => c.conflict && !me.picks.has(j));
  const box = $("meBody").querySelector(`[data-chunk="${i}"]`);
  for (const b of $("meBody").querySelectorAll(".current")) b.classList.remove("current");
  if (box) { box.classList.add("current"); box.scrollIntoView({ block: "center" }); const f = box.querySelector("button"); if (f) f.focus(); }
}
$("meNext").onclick = nextConflict;
