"use strict";
// TrunkLine's page: properties.
// One of the page's scripts (index.html loads them in order); they share one
// global scope, so core.js's helpers and state are used here as they are.

// ---- Properties --------------------------------------------------------------------------------

// What the dialog says about the properties svn knows. file/dir: what they
// can be set on; bool: on or off (svn stores "*"); choices: the values svn
// accepts; readonly: svn changes them itself.
const KNOWN_PROPS = {
  "svn:ignore": { dir: true, hint: "New files in this folder that svn shouldn't list: one name or pattern per line, e.g. *.o or build." },
  "svn:global-ignores": { dir: true, hint: "Like svn:ignore, but for this folder and every folder below it." },
  "svn:externals": { dir: true, hint: "Folders from elsewhere fetched into this one when updating, one per line: a URL and a folder name, e.g. ^/libs/common/trunk common" },
  "svn:needs-lock": { file: true, bool: true, hint: "The file stays read-only until someone locks it, so two people can't edit it at once. For files that can't be merged: CAD, images, Office documents." },
  "svn:executable": { file: true, bool: true, hint: "The file can be run as a program (on Linux and macOS)." },
  "svn:eol-style": { file: true, choices: ["native", "LF", "CRLF", "CR"], hint: "Line endings. native gives each person their system's (LF on Linux and macOS, CRLF on Windows)." },
  "svn:mime-type": { file: true, hint: "The file's type. application/octet-stream marks it binary (no diffs, merges or blame); text/plain marks it as text." },
  "svn:keywords": { file: true, hint: "Words svn fills in inside the file, e.g. Id Rev Date Author: $Id$ becomes the last change's details." },
  "svn:mergeinfo": { readonly: true, hint: "Records what was merged here. Merging changes it; it isn't edited by hand." },
  "svn:special": { readonly: true, hint: "Marks a symbolic link. svn sets it itself." },
};

let props = null; // the dialog's state: {rel, isDir, rows: [{name, orig, value, removed, custom}]}

async function openProps(rel) {
  let res;
  try { res = await call("Props", state.view.path, rel); } catch (e) { return report(e); }
  props = { rel, isDir: res.isDir, rows: res.props.map(p => ({ name: p.name, orig: p.value, value: p.value, removed: false })) };
  for (const r of props.rows) {
    if (r.name !== "svn:externals") continue;
    const x = await Go().ParseExternals(r.value);
    if (x.ok) r.ext = x.externals; else r.extNote = true; // comments or an unusual form: edit as text
  }
  showErrIn($("propsErr"), null);
  $("propsTitle").textContent = "Properties of " + (rel === "." ? "the top folder" : rel + (res.isDir ? "/" : ""));
  renderProps();
  for (;;) {
    const r = await ask($("propsDlg"));
    if (r !== "ok") { props = null; return; }
    let set;
    try { set = await propChanges(); } catch (e) { showErrIn($("propsErr"), typeof e === "string" ? { message: e } : e); continue; }
    if (!Object.keys(set).length) { props = null; return; }
    try {
      await call("SetProps", state.view.path, rel, set);
    } catch (e) { showErrIn($("propsErr"), e); continue; } // the dialog opens again with the edits kept
    props = null;
    state.checked.add(rel);
    state.known.add(rel);
    await refreshWc(false);
    if (state.diffPath === rel) { const c = state.changes.find(c => c.path === rel); if (c) showDiff(c); }
    const what = rel === "." ? "the top folder" : rel;
    setBanner("info", `The properties of ${what} changed: it's ticked, so commit it and everyone gets them.`);
    return;
  }
}

// propChanges is what to send to SetProps: name → new value, "" to remove.
// It throws a message for the user when something isn't filled in right.
async function propChanges() {
  // The externals table becomes svn:externals text (Go checks each row).
  for (const r of props.rows) {
    if (r.ext && !r.removed) r.value = await call("FormatExternals", r.ext);
  }
  const set = {};
  const seen = new Set();
  for (const r of props.rows) {
    const name = r.name.trim();
    if (r.removed) {
      if (r.orig !== null) set[name] = "";
      continue;
    }
    if (!name) throw "Give the new property a name, or remove it.";
    if (seen.has(name)) throw `${name} is in the list twice.`;
    seen.add(name);
    const k = KNOWN_PROPS[name] || {};
    if (k.readonly) continue;
    const value = k.bool ? "*" : r.value;
    if (!value.trim()) throw `Give ${name} a value, or remove it.`;
    if (value !== r.orig) set[name] = value;
  }
  return set;
}

function renderProps() {
  const list = $("propsList");
  list.replaceChildren();
  if (!props.rows.length) list.append(el("p", { class: "empty" }, "No properties set. Add one below."));
  for (const r of props.rows) {
    const k = KNOWN_PROPS[r.name] || {};
    const head = el("div", { class: "head" });
    if (r.custom) {
      const name = el("input", { class: "name", placeholder: "Property name, e.g. team:owner", "aria-label": "Property name", value: r.name, autocomplete: "off" });
      name.oninput = () => { r.name = name.value; };
      head.append(name);
    } else {
      head.append(el("span", { class: "name" }, r.name));
    }
    if (!k.readonly) {
      const rm = el("button", { type: "button", class: "ghost" }, r.removed ? "Keep" : "Remove");
      rm.onclick = () => {
        if (r.orig === null) props.rows.splice(props.rows.indexOf(r), 1); else r.removed = !r.removed;
        renderProps();
      };
      head.append(rm);
    }
    const box = el("div", { class: "prop" + (r.removed ? " removed" : "") }, head);
    if (k.bool) {
      box.append(el("span", {}, r.removed ? "Off" : "On"));
    } else if (k.choices) {
      const sel = el("select", { "aria-label": r.name, disabled: r.removed });
      const opts = k.choices.includes(r.value) || !r.value ? k.choices : [r.value, ...k.choices];
      sel.append(...opts.map(c => el("option", { value: c }, c)));
      sel.value = r.value || k.choices[0];
      r.value = sel.value;
      sel.onchange = () => { r.value = sel.value; };
      box.append(sel);
    } else if (r.name === "svn:externals" && r.ext && !r.removed) {
      box.append(externalsEditor(r));
    } else {
      if (r.extNote) box.append(el("div", { class: "hint" }, "Shown as text: it has comments or a form the table can't show."));
      const lines = r.value.split("\n").length;
      const ta = el("textarea", { "aria-label": (r.name || "New property") + " value", rows: Math.min(Math.max(lines, 1), 10), readonly: !!k.readonly || r.removed, spellcheck: "false" });
      ta.value = r.value;
      ta.oninput = () => { r.value = ta.value; };
      box.append(ta);
    }
    if (k.hint) box.append(el("div", { class: "hint" }, k.hint));
    list.append(box);
  }
  // The add menu: the properties svn knows that fit this file or folder and aren't set, and any other.
  const have = new Set(props.rows.map(r => r.name));
  const add = $("propsAdd");
  add.replaceChildren(el("option", { value: "" }, "Add a property…"),
    ...Object.entries(KNOWN_PROPS).filter(([n, k]) => !k.readonly && !have.has(n) && (props.isDir ? k.dir : k.file))
      .map(([n]) => el("option", { value: n }, n)),
    el("option", { value: "*" }, "Another (type its name)…"));
  add.value = "";
}

// externalsEditor is svn:externals as a table: a URL, a revision (empty for
// the latest) and the folder it goes in, per row.
function externalsEditor(r) {
  const tbody = el("tbody");
  const field = (x, key, attrs) => {
    const i = el("input", { autocomplete: "off", spellcheck: "false", value: x[key], ...attrs });
    i.oninput = () => { x[key] = i.value; };
    return i;
  };
  for (const x of r.ext) {
    tbody.append(el("tr", {},
      el("td", {}, field(x, "url", { placeholder: "URL, e.g. ^/libs/common/trunk", "aria-label": "External URL" })),
      el("td", { class: "rev" }, field(x, "rev", { placeholder: "latest", inputmode: "numeric", "aria-label": "Revision (empty for the latest)" })),
      el("td", {}, field(x, "dir", { placeholder: "folder name", "aria-label": "Folder it goes in" })),
      el("td", { class: "rm" }, el("button", { type: "button", class: "ghost", "aria-label": "Remove this external",
        onclick: () => { r.ext.splice(r.ext.indexOf(x), 1); renderProps(); } }, "×"))));
  }
  const add = el("button", { type: "button", class: "ghost", onclick: () => {
    r.ext.push({ url: "", rev: "", dir: "" });
    renderProps();
    const rows = $("propsList").querySelectorAll(".ext tbody tr");
    const last = rows[rows.length - 1];
    if (last) last.querySelector("input").focus();
  } }, "+ Add an external");
  const asText = el("button", { type: "button", class: "ghost", onclick: async () => {
    try {
      r.value = r.ext.length ? await call("FormatExternals", r.ext) : "";
    } catch (e) { return showErrIn($("propsErr"), e); }
    r.ext = null;
    showErrIn($("propsErr"), null);
    renderProps();
  } }, "Edit as text");
  return el("div", { class: "ext" },
    el("table", {}, el("thead", {}, el("tr", {}, el("td", { class: "muted" }, "URL"), el("td", { class: "muted" }, "Revision"),
      el("td", { class: "muted" }, "Folder"), el("td", {}))), tbody),
    el("div", { class: "bar" }, add, asText));
}

$("propsAdd").onchange = () => {
  const v = $("propsAdd").value;
  if (!v || !props) return;
  const custom = v === "*";
  props.rows.push({ name: custom ? "" : v, orig: null, value: "", removed: false, custom, ext: v === "svn:externals" ? [{ url: "", rev: "", dir: "" }] : undefined });
  renderProps();
  const last = $("propsList").lastElementChild;
  const f = last && last.querySelector(custom ? "input" : "textarea, select");
  if (f) f.focus();
};

$("propsFileBtn").onclick = () => state.diffPath && openProps(state.diffPath);
$("propsTopBtn").onclick = () => state.view && openProps(".");
