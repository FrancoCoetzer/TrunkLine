"use strict";
// TrunkLine's page logic. Every svn call goes through call(), which turns the
// Go side's JSON errors into a login or certificate dialog and retries.

const $ = id => document.getElementById(id);
const Go = () => window.go.main.App;

const state = {
  startup: null,
  wcs: [],
  servers: [],
  view: null,          // {type: "wc", path} or {type: "browse", root, url, server}
  info: null,          // svn info of the open working copy
  changes: [],
  checked: new Set(),  // ticked change paths
  known: new Set(),    // paths seen before (new ones get the default tick)
  diffPath: null,
  log: [],
  logRev: null,
  incoming: false,
};

// ---- Small helpers ----------------------------------------------------------

function el(tag, attrs = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e.addEventListener(k.slice(2), v);
    else if (v !== undefined && v !== null && v !== false) e.setAttribute(k, v === true ? "" : v);
  }
  for (const k of kids) if (k !== null && k !== undefined) e.append(k);
  return e;
}

function store(key, value) {
  try {
    if (value === undefined) return localStorage.getItem(key);
    localStorage.setItem(key, value);
  } catch { /* storage off: the app still works */ }
  return null;
}

// ---- Messages (toasts) ------------------------------------------------------------------------
//
// toast(msg, cls) shows a message in the bottom-right stack: cls "ok", "err"
// or "" (info). While a dialog is open the message waits and appears when it
// closes (it would be hidden behind it, and easy to miss). Errors stay until
// dismissed; others go after a few seconds, but not while the pointer or
// keyboard focus is on them. Each is announced to screen readers when shown.

const toastQueue = [];
let toastWaiting = null;
const TOAST_SECONDS = 7;

function modalOpen() { return !!document.querySelector("dialog[open]"); }

function toast(msg, cls = "") {
  msg = String(msg);
  // the same message twice in a row: show it once
  const last = toastQueue[toastQueue.length - 1];
  if (last && last.msg === msg) return;
  toastQueue.push({ msg, cls });
  if (toastQueue.length > 5) toastQueue.shift();
  pumpToasts();
}

function pumpToasts() {
  if (!toastQueue.length) return;
  if (modalOpen()) {
    // A timer, not the dialog's close event: a webview that isn't painting can hold that back.
    if (!toastWaiting) toastWaiting = setInterval(() => {
      if (modalOpen()) return;
      clearInterval(toastWaiting);
      toastWaiting = null;
      pumpToasts();
    }, 250);
    return;
  }
  while (toastQueue.length) showToast(toastQueue.shift());
}

// splitError separates svn's own lines ("svn: E170013: ...") from the
// explanation TrunkLine wrote, so the toast can lead with plain words.
function splitError(msg) {
  const lines = msg.split("\n");
  const raw = lines.filter(l => /^svn: /.test(l));
  let summary = lines.filter(l => !/^svn: /.test(l)).join("\n").trim();
  if (!summary && raw.length) {
    summary = raw[raw.length - 1].replace(/^svn: (warning: )?[EW]\d{6}: /, "");
    if (raw.length === 1) return { summary, details: "" };
  }
  return { summary: summary || msg, details: raw.join("\n") };
}

function announce(text, urgent) {
  const region = $(urgent ? "announceAlert" : "announcePolite");
  region.textContent = "";
  setTimeout(() => (region.textContent = text), 50); // a change, so a repeated message is read again
}

function showToast({ msg, cls }) {
  const stack = $("toasts");
  while (stack.children.length >= 3) dismissToast(stack.firstElementChild, false);
  const { summary, details } = cls === "err" ? splitError(msg) : { summary: msg, details: "" };
  const body = el("div", { class: "toast-body" }, el("p", { class: "toast-msg" }, summary));
  if (details) {
    const copy = el("button", { type: "button", class: "ghost" }, "Copy");
    copy.onclick = () => {
      navigator.clipboard.writeText(msg).then(() => (copy.textContent = "Copied"), () => (copy.textContent = "Couldn't copy"));
    };
    body.append(el("details", {}, el("summary", {}, "Details"), el("pre", {}, details), copy));
  }
  const close = el("button", { type: "button", class: "ghost toast-close", "aria-label": "Dismiss this message", title: "Dismiss (Esc)" }, "×");
  const t = el("div", { class: "toast " + cls }, body, close);
  close.onclick = () => dismissToast(t, true);
  stack.append(t);
  announce(summary, cls === "err");
  if (cls !== "err") {
    const tick = () => {
      if (!t.isConnected) return;
      if (t.matches(":hover, :focus-within")) { t._timer = setTimeout(tick, 1000); return; }
      dismissToast(t, false);
    };
    t._timer = setTimeout(tick, TOAST_SECONDS * 1000);
  }
}

// dismissToast removes a toast; keyboard focus inside it moves to the next
// one's close button, or back to the page.
function dismissToast(t, byUser) {
  if (!t || !t.isConnected || t.classList.contains("leaving")) return;
  clearTimeout(t._timer);
  const hadFocus = t.contains(document.activeElement);
  t.classList.add("leaving");
  setTimeout(() => t.remove(), 140); // a timer, not transitionend (see the motion notes in index.html)
  if (hadFocus || byUser) {
    const next = [...$("toasts").children].reverse().find(x => x !== t && !x.classList.contains("leaving"));
    if (hadFocus) (next ? next.querySelector(".toast-close") : document.querySelector("main"))?.focus?.();
  }
}

document.addEventListener("keydown", e => {
  if (e.key !== "Escape" || modalOpen()) return;
  const last = [...$("toasts").children].reverse().find(x => !x.classList.contains("leaving"));
  if (last) { e.preventDefault(); dismissToast(last, true); }
});

function fmtDate(s) {
  if (!s || s.startsWith("0001")) return "";
  const d = new Date(s);
  const pad = n => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fmtSize(n) {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i ? n.toFixed(1) : n) + " " + u[i];
}

function sep() { return state.startup && state.startup.os === "windows" ? "\\" : "/"; }

// readable shows a repository URL or path (svn gives them URL-encoded:
// caf%C3%A9.txt) the way people write it (café.txt).
function readable(u) {
  try { return decodeURI(u); } catch { return u; }
}

// encodePath makes a repository path safe to put in a URL.
function encodePath(p) { return p.split("/").map(encodeURIComponent).join("/"); }

// ---- Dialogs -------------------------------------------------------------------

// ask opens a dialog and resolves with the value of the button that closed
// it ("ok", another submit button's value) or "cancel". It listens for the
// form's submit and the dialog's cancel (Esc) rather than the close event,
// which a webview that is not painting (minimised, hidden) can hold back.
function ask(dlg) {
  return new Promise(resolve => {
    const form = dlg.querySelector("form");
    const done = v => {
      form.removeEventListener("submit", onSubmit);
      dlg.removeEventListener("cancel", onCancel);
      if (dlg.open) dlg.close(v);
      resolve(v);
    };
    const onSubmit = e => { e.preventDefault(); done(e.submitter && e.submitter.value || "cancel"); };
    const onCancel = e => { e.preventDefault(); done("cancel"); };
    form.addEventListener("submit", onSubmit);
    dlg.addEventListener("cancel", onCancel);
    dlg.returnValue = "";
    dlg.showModal();
  });
}

// Cancel buttons must not be the form's default (Enter would press them);
// they cancel like Esc does.
for (const b of document.querySelectorAll('dialog button[value="cancel"]')) {
  b.type = "button";
  b.addEventListener("click", () => {
    const dlg = b.closest("dialog");
    if (dlg.dispatchEvent(new Event("cancel", { cancelable: true }))) dlg.close("cancel");
  });
}

function confirmDanger(title, msg, okLabel) {
  $("confirmTitle").textContent = title;
  $("confirmMsg").textContent = msg;
  $("confirmOk").textContent = okLabel;
  return ask($("confirmDlg")).then(r => r === "ok");
}

async function askLogin(err) {
  $("loginHost").textContent = err.host;
  const wrong = /E170001|authorization failed/i.test(err.message) && !/E215004/.test(err.message);
  $("loginWhy").textContent = wrong ? "The server refused that username or password." : "This server needs a login.";
  $("loginUser").value = (await Go().LastUsername(err.host)) || "";
  $("loginPass").value = "";
  const r = ask($("loginDlg"));
  ($("loginUser").value ? $("loginPass") : $("loginUser")).focus();
  if (await r !== "ok") return false;
  await Go().Login(err.host, $("loginUser").value.trim(), $("loginPass").value);
  return true;
}

async function askTrust(err) {
  $("certHost").textContent = err.host;
  $("certMsg").textContent = err.message;
  if (await ask($("certDlg")) !== "ok") return false;
  await Go().TrustHost(err.host);
  return true;
}

function parseErr(e) {
  if (e && typeof e === "object" && e.kind) return e;
  const s = String(e && e.message !== undefined ? e.message : e);
  try {
    const j = JSON.parse(s);
    if (j && j.kind) return j;
  } catch { /* not ours */ }
  return { kind: "other", message: s };
}

// call runs a Go method; a login or certificate problem asks the user and
// tries again, anything else is thrown as {kind, message}.
// The thin bar at the top shows while any call is waiting on svn, after 150ms
// (so quick ones don't flicker it). Background checks don't use call().
let inFlight = 0, busyTimer = null;
function busyStart() {
  if (inFlight++ > 0) return;
  busyTimer = setTimeout(() => {
    $("busyBar").classList.add("on");
    document.querySelector("main").setAttribute("aria-busy", "true");
  }, 150);
}
function busyEnd() {
  if (--inFlight > 0) return;
  clearTimeout(busyTimer);
  $("busyBar").classList.remove("on");
  document.querySelector("main").removeAttribute("aria-busy");
}

async function call(name, ...args) {
  let logins = 0;
  for (;;) {
    try {
      busyStart();
      try {
        return await Go()[name](...args);
      } finally {
        busyEnd();
      }
    } catch (e) {
      const err = parseErr(e);
      if (err.kind === "auth" && err.host && logins++ < 5 && await askLogin(err)) continue;
      if (err.kind === "cert" && err.host && await askTrust(err)) continue;
      throw err;
    }
  }
}

// report shows a failed call, with a hint for the usual fixes.
function report(err) {
  err = parseErr(err);
  if (err.kind === "canceled") return; // the user knows
  let msg = err.message;
  if (err.kind === "locked") msg += "\n\nTry Cleanup.";
  if (err.kind === "conflict" && /out of date/i.test(msg)) msg += "\n\nSomeone committed first: Update, then commit again.";
  if (err.kind === "auth" || err.kind === "cert") msg = "Not connected: " + msg;
  toast(msg, "err");
}

// flow runs one user action that opens dialogs or waits on the server
// (merge, switch, checkout, lock…) at a time: a second click while one is
// in progress is ignored instead of starting it twice.
let flowRunning = false;
async function flow(f) {
  if (flowRunning) return;
  flowRunning = true;
  try {
    return await f();
  } catch (e) {
    report(e);
  } finally {
    flowRunning = false;
  }
}

async function busy(btn, f) {
  const was = btn.disabled;
  btn.disabled = true;
  btn.setAttribute("aria-busy", "true");
  try { return await f(); } catch (e) { report(e); } finally { btn.disabled = was; btn.removeAttribute("aria-busy"); }
}

// ---- Progress (checkout and update) -------------------------------------------------

let progLines = [];
let progFlush = null;
window.runtime.EventsOn("op:line", line => {
  progLines.push(line);
  if (!progFlush) progFlush = setTimeout(flushProgress, 50);
});

function flushProgress() {
  clearTimeout(progFlush);
  progFlush = null;
  if (!progLines.length) return;
  const log = $("progLog");
  log.textContent += progLines.join("\n") + "\n";
  progLines = [];
  if (log.textContent.length > 400000) log.textContent = "…\n" + log.textContent.slice(-300000);
  log.scrollTop = log.scrollHeight;
}

// withProgress shows the progress dialog while f runs.
async function withProgress(title, f) {
  $("progTitle").textContent = title;
  $("progLog").textContent = "";
  $("progClose").disabled = true;
  $("progCancel").disabled = false;
  $("progBar").classList.add("on");
  const dlg = $("progDlg");
  dlg.showModal();
  try {
    const r = await f();
    flushProgress();
    $("progTitle").textContent = title + ": done";
    return r;
  } catch (e) {
    flushProgress();
    const err = parseErr(e);
    if (!$("progLog").textContent) {
      // refused before svn printed anything: the toast says why
      $("progClose").disabled = false;
      dlg.close();
      throw err;
    }
    $("progTitle").textContent = title + (err.kind === "canceled" ? ": canceled" : ": failed");
    if (err.kind !== "canceled") $("progLog").textContent += "\n" + err.message + "\n";
    throw err;
  } finally {
    $("progClose").disabled = false;
    $("progCancel").disabled = true;
    $("progBar").classList.remove("on");
  }
}
$("progCancel").onclick = () => Go().Cancel();
$("progDlg").addEventListener("cancel", e => { if ($("progClose").disabled) e.preventDefault(); });

// ---- Keyboard: moving through list rows ------------------------------------------------------
//
// A list is one Tab stop: one row is focusable at a time (a roving tabindex).
// Arrow keys, Home and End move between rows and run the row's _move (show
// its diff or revision); Space runs _space (tick it); Enter runs _enter
// (open it). Checkboxes in such rows leave the Tab order: Space ticks them.

function rovingRows(tb, refocus) {
  const rows = [...tb.querySelectorAll(":scope > tr")].filter(r => r._move || r._space || r._enter);
  if (!rows.length) return;
  const cur = rows.find(r => r.dataset.key && r.dataset.key === tb._lastKey) || rows.find(r => r.classList.contains("sel")) || rows[0];
  for (const r of rows) {
    r.tabIndex = r === cur ? 0 : -1;
    for (const b of r.querySelectorAll("input[type=checkbox]")) b.tabIndex = -1;
  }
  if (refocus) cur.focus();
  if (tb._roving) return;
  tb._roving = true;
  tb.addEventListener("focusin", e => {
    const r = e.target.closest("tr");
    if (r && r.parentElement === tb && r.dataset.key) tb._lastKey = r.dataset.key;
  });
  tb.addEventListener("click", e => {
    const r = e.target.closest("tr");
    if (!r || r.parentElement !== tb || !(r._move || r._space || r._enter)) return;
    for (const x of tb.querySelectorAll(":scope > tr")) if (x.tabIndex === 0) x.tabIndex = -1;
    r.tabIndex = 0;
    if (r.dataset.key) tb._lastKey = r.dataset.key;
  });
  tb.addEventListener("keydown", e => {
    const rows = [...tb.querySelectorAll(":scope > tr")].filter(r => r._move || r._space || r._enter);
    const i = rows.indexOf(document.activeElement);
    if (i < 0 || e.ctrlKey || e.metaKey || e.altKey) return;
    const row = rows[i];
    const to = { ArrowDown: Math.min(i + 1, rows.length - 1), ArrowUp: Math.max(i - 1, 0), Home: 0, End: rows.length - 1 }[e.key];
    if (to !== undefined) {
      e.preventDefault();
      for (const r of rows) r.tabIndex = -1;
      rows[to].tabIndex = 0;
      rows[to].focus();
      rows[to].scrollIntoView({ block: "nearest" });
      if (rows[to]._move) rows[to]._move();
    } else if (e.key === " " && row._space) {
      e.preventDefault();
      row._space();
    } else if (e.key === "Enter" && row._enter) {
      e.preventDefault();
      row._enter();
    }
  });
}

// lockRow and mergeRow make a row from cells whose first cell holds box:
// Space ticks it; in the merge list a click anywhere on the row does too.
function lockRow(f, box, ...cells) {
  const tr = el("tr", {}, ...cells);
  tr.dataset.key = f.path;
  tr._space = () => box.click();
  return tr;
}
function mergeRow(box, ...cells) {
  const tr = el("tr", {}, ...cells);
  tr.dataset.key = box.dataset.rev;
  tr._space = () => box.click();
  tr.addEventListener("click", e => { if (e.target !== box) box.click(); });
  return tr;
}

// ---- The banner above the commit box --------------------------------------------------------
//
// Guidance the user needs to act on (a merge to commit, conflicts to resolve)
// stays here until dealt with, instead of in a message that goes away.

state.banner = null; // {kind: "conflict"|"info", text}
function setBanner(kind, text) {
  state.banner = { kind, text };
  $("wcBannerText").textContent = text;
  $("wcBanner").className = "banner inline " + (kind === "conflict" ? "err" : "info");
  $("wcBanner").hidden = false;
}
function clearBanner() {
  state.banner = null;
  $("wcBanner").hidden = true;
}
$("wcBannerClose").onclick = clearBanner;

// ---- Menus: the menu bar and the ⋯ menu of the working copy bar ----------------------------------
//
// A menu is a popover of buttons under its trigger button. ↑/↓, Home and End
// move between its items (disabled ones skipped); Esc closes it.

// zoomScale is how many of getBoundingClientRect's pixels a fixed element's
// CSS pixel (top, left…) becomes, which the page zoom changes, and engines
// count differently: so it's measured.
function zoomScale() {
  const probe = el("div", { style: "position:fixed;top:100px;left:0;width:1px;height:1px;visibility:hidden" });
  document.body.append(probe);
  const z = probe.getBoundingClientRect().top / 100;
  probe.remove();
  return z > 0 ? z : 1;
}

function menuItems(menu) { return [...menu.querySelectorAll("button")].filter(b => !b.disabled); }

// setupMenu wires menu to its trigger btn; align "left" or "right" lines up
// that edge of the menu with the button's.
function setupMenu(menu, btn, align) {
  menu.addEventListener("beforetoggle", e => {
    if (e.newState !== "open") return;
    if (menu._prepare) menu._prepare();
    const r = btn.getBoundingClientRect(), z = zoomScale();
    menu.style.top = Math.round((r.bottom + 4) / z) + "px";
    if (align === "left") {
      menu.style.left = Math.round(r.left / z) + "px";
      menu.style.right = "auto";
    } else {
      menu.style.right = Math.round((document.documentElement.getBoundingClientRect().right - r.right) / z) + "px";
      menu.style.left = "auto";
    }
  });
  menu.addEventListener("toggle", e => {
    const open = e.newState === "open";
    btn.setAttribute("aria-expanded", open ? "true" : "false");
    if (open && (menu._focusFirst || btn.matches(":focus-visible"))) {
      const first = menuItems(menu)[0];
      if (first) first.focus();
    }
    // Closed from the keyboard (Esc, or an item): focus goes back to the button.
    if (!open && menu._focusFirst !== undefined && (menu.contains(document.activeElement) || document.activeElement === document.body)) btn.focus();
    menu._focusFirst = open ? menu._focusFirst : undefined;
  });
  menu.addEventListener("click", e => {
    const b = e.target.closest("button");
    if (!b || b.disabled) return;
    menu.hidePopover();
    if (b.dataset.cmd && commands[b.dataset.cmd]) commands[b.dataset.cmd](b);
  });
  menu.addEventListener("keydown", e => {
    if (menu._bar !== undefined && (e.key === "ArrowRight" || e.key === "ArrowLeft")) {
      e.preventDefault();
      openBarMenu(menu._bar + (e.key === "ArrowRight" ? 1 : -1), true);
      return;
    }
    const items = menuItems(menu);
    const i = items.indexOf(document.activeElement);
    const to = { ArrowDown: (i + 1) % items.length, ArrowUp: (i - 1 + items.length) % items.length, Home: 0, End: items.length - 1 }[e.key];
    if (to === undefined || i < 0) return;
    e.preventDefault();
    items[to].focus();
  });
}
setupMenu($("wcMenu"), $("wcMenuBtn"), "right");

// The menu bar: File, View, Help.
const BAR = [["mbFile", "menuFile"], ["mbView", "menuView"], ["mbHelp", "menuHelp"]];
BAR.forEach(([b, m], i) => {
  setupMenu($(m), $(b), "left");
  $(m)._bar = i;
  // With one menu open, pointing at another opens that one instead.
  $(b).addEventListener("pointerenter", () => {
    if (BAR.some(([, o]) => o !== m && $(o).matches(":popover-open"))) openBarMenu(i, false);
  });
});

// openBarMenu opens the i-th menu of the menu bar (wrapping around); focus
// puts the keyboard on its first item.
function openBarMenu(i, focus) {
  const [b, m] = BAR[(i + BAR.length) % BAR.length];
  for (const [, o] of BAR) if (o !== m && $(o).matches(":popover-open")) { $(o)._focusFirst = undefined; $(o).hidePopover(); }
  $(m)._focusFirst = focus;
  if (!$(m).matches(":popover-open")) $(m).showPopover();
  else if (focus) { const f = menuItems($(m))[0]; if (f) f.focus(); }
  if (!focus) $(b).focus({ preventScroll: true });
}

// The View menu shows what's on: the tab, unversioned files, the theme.
$("menuView")._prepare = () => {
  const wc = !!(state.view && state.view.type === "wc");
  for (const b of $("menuView").querySelectorAll("[data-wc]")) b.disabled = !wc;
  for (const b of $("menuView").querySelectorAll('[data-cmd="tab"]')) b.setAttribute("aria-checked", String(wc && currentTab() === b.dataset.tab));
  $("menuView").querySelector('[data-cmd="unversioned"]').setAttribute("aria-checked", String($("showUnversioned").checked));
  $("menuView").querySelector('[data-cmd="sbs"]').setAttribute("aria-checked", String(sideBySide()));
  for (const b of $("menuView").querySelectorAll('[data-cmd="theme"]')) b.setAttribute("aria-checked", String(look.theme === b.dataset.theme));
  $("menuView").querySelector('[data-cmd="refresh"]').disabled = !state.view;
};

// Alt+F, Alt+V, Alt+H open the menus; holding Alt underlines those letters.
document.addEventListener("keydown", e => {
  document.body.classList.toggle("alt", e.key === "Alt");
  if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || modalOpen()) return;
  const i = { f: 0, v: 1, h: 2 }[e.key.toLowerCase()];
  if (i === undefined) return;
  e.preventDefault();
  openBarMenu(i, true);
});
document.addEventListener("keyup", () => document.body.classList.remove("alt"));
window.addEventListener("blur", () => document.body.classList.remove("alt"));

// The "N incoming" chip asks the server again.
$("wcIncoming").onclick = () => busy($("wcIncoming"), () => refreshWc(true));

// ---- Sidebar -------------------------------------------------------------------------

// shortPath shows a folder for the sidebar: the home folder as ~, and a long
// path shortened in the middle so its last folders (the name) stay visible.
function shortPath(p) {
  const home = state.startup && state.startup.home;
  if (home && (p === home || p.startsWith(home + sep()))) p = "~" + p.slice(home.length);
  const parts = p.split(/[\\/]/);
  if (p.length <= 34 || parts.length <= 3) return p;
  return parts[0] + sep() + "…" + sep() + parts.slice(-2).join(sep());
}

function renderSidebar() {
  const wcList = $("wcList");
  wcList.replaceChildren();
  if (!state.wcs.length) wcList.append(el("div", { class: "empty" }, "None yet"));
  for (const wc of state.wcs) {
    const active = state.view && state.view.type === "wc" && state.view.path === wc.path;
    wcList.append(el("button", { class: "navitem" + (active ? " active" : ""), title: wc.path, "aria-current": active ? "page" : null,
      onclick: () => openWc(wc.path) }, wc.name, el("span", { class: "sub" }, shortPath(wc.path))));
  }
  const sl = $("serverList");
  sl.replaceChildren();
  if (!state.servers.length) sl.append(el("div", { class: "empty" }, "None yet"));
  for (const s of state.servers) {
    const active = state.view && state.view.type === "browse" && state.view.server === s.url;
    sl.append(el("button", { class: "navitem" + (active ? " active" : ""), title: s.url, "aria-current": active ? "page" : null,
      onclick: () => openBrowse(s.url, s.url, s.name) },
      s.name, el("span", { class: "sub" }, s.url)));
  }
}

function showView(which) {
  $("welcome").hidden = which !== "welcome";
  $("wcView").hidden = which !== "wc";
  $("browseView").hidden = which !== "browse";
}

async function reloadLists() {
  const s = await Go().Startup();
  state.wcs = s.workingCopies;
  state.servers = s.servers;
  renderSidebar();
}

async function addWorkingCopy() {
  const dir = await Go().PickFolder("Pick a folder you checked out");
  if (!dir) return;
  try {
    const wc = await call("AddWorkingCopy", dir);
    await reloadLists();
    openWc(wc.path);
  } catch (e) { report(e); }
}

async function addServer() {
  $("serverUrl").value = "";
  $("serverName").value = "";
  const r = ask($("serverDlg"));
  $("serverUrl").focus();
  if (await r !== "ok") return;
  const url = $("serverUrl").value.trim();
  if (!url) return;
  try {
    await call("AddServer", $("serverName").value.trim(), url);
    await reloadLists();
    const s = state.servers.find(s => s.url === url.replace(/\/+$/, ""));
    if (s) openBrowse(s.url, s.url, s.name);
  } catch (e) { report(e); }
}

$("addWcBtn").onclick = addWorkingCopy;
$("welcomeWc").onclick = addWorkingCopy;
$("addServerBtn").onclick = addServer;
$("welcomeServer").onclick = addServer;

// ---- A working copy ------------------------------------------------------------------

async function openWc(path) {
  if (!(state.view && state.view.type === "wc" && state.view.path === path)) {
    state.checked = new Set();
    state.known = new Set();
    state.diffPath = null;
    state.log = [];
    state.logRev = null;
    state.incoming = false;
    state.shownPaths = null;
    clearBanner();
    setIncoming(0);
    setTimeout(checkIncoming, 1500);
    state.locks = [];
    state.lockChecked = new Set();
    state.branches = null;
    $("commitMsg").value = "";
    $("diff").replaceChildren();
    $("diffTitle").textContent = "Pick a file to see its changes";
    $("openFileBtn").hidden = true;
    $("blameFileBtn").hidden = true;
    $("historyFileBtn").hidden = true;
    state.historyFile = null;
    state.logSearch = "";
    $("logSearch").value = "";
    // Nothing of the previous working copy may show while this one loads.
    $("changes").replaceChildren(el("tr", {}, el("td", { class: "placeholder", colspan: 4 }, "Loading…")));
    $("changeCount").textContent = "";
    for (const id of ["log", "branches", "locks", "revDiff"]) $(id).replaceChildren();
    $("revDetail").hidden = true;
    $("wcIncoming").hidden = true;
  }
  state.view = { type: "wc", path };
  store("last", JSON.stringify(state.view));
  const wc = state.wcs.find(w => w.path === path);
  $("wcName").textContent = wc ? wc.name : path;
  $("wcUrl").textContent = wc ? wc.url : "";
  $("wcRev").textContent = "";
  $("wcIncoming").hidden = true;
  renderSidebar();
  showView("wc");
  setTab(currentTab());
  await refreshWc(false);
}

async function refreshWc(remote) {
  const path = state.view.path;
  try {
    const [info, st] = await Promise.all([call("Info", path), call("Status", path, remote)]);
    if (!state.view || state.view.path !== path) return;
    state.info = info;
    $("wcUrl").textContent = readable(info.relativeUrl || info.url);
    showBranchChip(info.relativeUrl || "");
    $("wcUrl").title = info.url;
    $("wcRev").textContent = "r" + info.revision;
    state.changes = st.changes;
    if (remote) {
      state.incoming = true;
      const n = st.changes.filter(c => c.repoItem).length;
      setIncoming(n);
      $("wcIncoming").textContent = n ? `${n} incoming (HEAD r${st.head})` : `up to date with r${st.head}`;
      $("wcIncoming").hidden = false;
    }
    renderChanges();
    if (currentTab() === "history" && !state.log.length) loadLog(true);
  } catch (e) {
    report(e);
  }
}

function stInfo(ch) {
  const items = {
    modified: "M", added: "A", deleted: "D", replaced: "R", unversioned: "?", missing: "!",
    conflicted: "C", obstructed: "~", external: "X", incomplete: "!", ignored: "I",
  };
  if (ch.treeConflict) return { letter: "C", cls: "conflicted", label: "tree conflict" };
  if (ch.props === "conflicted") return { letter: "C", cls: "conflicted", label: "property conflict" };
  if (items[ch.item]) return { letter: items[ch.item], cls: ch.item, label: ch.item };
  if (ch.props === "modified") return { letter: "P", cls: "props", label: "properties modified" };
  if (ch.locked) return { letter: "L", cls: "missing", label: "locked: run Cleanup" };
  return { letter: "", cls: "", label: "" };
}

// localChange says whether the row has something to commit or revert (not
// just an incoming change from the server).
function localChange(ch) {
  return ch.item !== "normal" && ch.item !== "external" && ch.item !== "none" || ch.props === "modified" || ch.props === "conflicted" || ch.treeConflict;
}

function visibleChanges() {
  const showUnv = $("showUnversioned").checked;
  return state.changes.filter(c => showUnv || c.item !== "unversioned");
}

function renderChanges() {
  const hadFocus = $("changes").contains(document.activeElement);
  const rows = visibleChanges();
  // Rows that weren't listed last time get a brief highlight (not on the first listing).
  const before = state.shownPaths;
  state.shownPaths = new Set(rows.map(c => c.path));
  for (const c of state.changes) {
    if (!state.known.has(c.path)) {
      state.known.add(c.path);
      if (localChange(c) && c.item !== "unversioned") state.checked.add(c.path);
    }
  }
  const paths = new Set(state.changes.map(c => c.path));
  for (const p of state.checked) if (!paths.has(p)) state.checked.delete(p);

  const tb = $("changes");
  tb.replaceChildren();
  // Files in changelists come after the others, under a heading per changelist.
  const lists = [...new Set(rows.map(c => c.changelist).filter(Boolean))].sort((a, b) => a.localeCompare(b));
  const groups = lists.length ? [["", rows.filter(c => !c.changelist)], ...lists.map(n => [n, rows.filter(c => c.changelist === n)])] : [["", rows]];
  for (const [name, members] of groups) {
    if (lists.length && members.length) tb.append(groupRow(name, members));
    for (const ch of members) tb.append(changeRow(ch, before, lists.length > 0));
  }
  if (!rows.length) tb.append(el("tr", {}, el("td", { colspan: 4, class: "placeholder" }, "No local changes")));
  rovingRows(tb, hadFocus);
  afterChanges();
}

// groupRow is the heading of a changelist in the Changes list (name "": the
// files in none), with a box that ticks the whole group.
function groupRow(name, members) {
  const can = members.filter(localChange);
  const box = el("input", { type: "checkbox", disabled: !can.length, "aria-label": "Include every file " + (name ? "in changelist " + name : "not in a changelist") });
  box.checked = can.length > 0 && can.every(c => state.checked.has(c.path));
  box.indeterminate = !box.checked && can.some(c => state.checked.has(c.path));
  box.onclick = e => {
    e.stopPropagation();
    for (const c of can) if (box.checked) state.checked.add(c.path); else state.checked.delete(c.path);
    renderChanges();
  };
  box._sync = () => {
    box.checked = can.length > 0 && can.every(c => state.checked.has(c.path));
    box.indeterminate = !box.checked && can.some(c => state.checked.has(c.path));
  };
  const n = members.length === 1 ? "1 file" : members.length + " files";
  const cells = [el("td", {}, box), el("td", { colspan: 3 },
    name ? el("span", { class: "clname" }, name) : el("span", {}, "Not in a changelist"), name ? n : "")];
  if (name) {
    cells[1].append(el("button", {
      type: "button", class: "ghost", title: "Take every file out of this changelist (the changes stay)",
      onclick: e => { e.stopPropagation(); setChangelist(members, ""); },
    }, "Ungroup"));
  }
  const tr = el("tr", { class: "group" }, ...cells);
  tr._sync = box._sync;
  tr.dataset.key = "changelist:" + name;
  tr._space = () => box.click();
  return tr;
}

function changeRow(ch, before, grouped) {
  const si = stInfo(ch);
  const can = localChange(ch);
  const box = el("input", { type: "checkbox", disabled: !can, "aria-label": "Include " + ch.path });
  box.checked = can && state.checked.has(ch.path);
  box.onclick = e => {
    e.stopPropagation();
    if (box.checked) state.checked.add(ch.path); else state.checked.delete(ch.path);
    updateChangeButtons();
  };
  const fresh = before && !before.has(ch.path) ? " fresh" : "";
  const tr = el("tr", { class: (ch.path === state.diffPath ? "sel" : "") + fresh + (grouped && ch.changelist ? " ingroup" : ""), onclick: () => showDiff(ch) },
    el("td", {}, box),
    el("td", {}, el("span", { class: "st st-" + si.cls, title: si.label, "aria-hidden": "true" }, si.letter),
      el("span", { class: "sr-only" }, si.label)),
    el("td", {}, ch.repoItem ? el("span", { class: "incoming", title: "changed on the server: " + ch.repoItem }, "*") : ""),
    el("td", { class: "path", title: ch.path + (ch.changelist ? "  (changelist " + ch.changelist + ")" : "") },
      ch.path + (ch.isDir ? "/" : ""), lockMark(ch.lockState, ch.lock)));
  tr.ondblclick = () => Go().Open(state.view.path, ch.path);
  tr.dataset.key = ch.path;
  tr._move = () => showDiff(ch);
  if (can) tr._space = () => box.click();
  tr._enter = () => Go().Open(state.view.path, ch.path);
  return tr;
}

function afterChanges() {
  // The file whose diff is shown has gone from the list (committed, reverted, ignored): clear the diff.
  if (state.diffPath && !state.changes.some(c => c.path === state.diffPath)) {
    state.diffPath = null;
    $("diff").replaceChildren();
    $("diffTitle").textContent = "Pick a file to see its changes";
    for (const id of ["openFileBtn", "blameFileBtn", "historyFileBtn", "propsFileBtn"]) $(id).hidden = true;
  }
  // The banner's guidance is done with once there's nothing left to act on.
  if (state.banner && state.banner.kind === "conflict" && !state.changes.some(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict)) clearBanner();
  if (state.banner && !state.changes.some(c => localChange(c) && c.item !== "unversioned")) clearBanner();
  const local = state.changes.filter(localChange).length;
  $("changeCount").textContent = local === 1 ? "1 change" : local + " changes";
  updateChangeButtons();
}

function checkedChanges() {
  return state.changes.filter(c => state.checked.has(c.path) && localChange(c));
}

function updateChangeButtons() {
  const sel = checkedChanges();
  const rows = visibleChanges().filter(localChange);
  $("selAll").checked = rows.length > 0 && rows.every(c => state.checked.has(c.path));
  for (const tr of $("changes").querySelectorAll(":scope > tr.group")) tr._sync();
  $("commitBtn").disabled = !sel.length;
  $("revertBtn").disabled = !sel.some(c => c.item !== "unversioned");
  $("addBtn").disabled = !sel.some(c => c.item === "unversioned");
  $("ignoreBtn").disabled = !sel.some(c => c.item === "unversioned");
  $("changelistBtn").disabled = !sel.some(canChangelist);
  $("resolveBox").hidden = !state.changes.some(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict);
  $("keepLocksBox").hidden = !sel.some(c => c.lockState === "mine");
}

$("selAll").onchange = () => {
  for (const c of visibleChanges().filter(localChange)) {
    if ($("selAll").checked) state.checked.add(c.path); else state.checked.delete(c.path);
  }
  renderChanges();
};
$("showUnversioned").onchange = () => {
  store("showUnversioned", $("showUnversioned").checked ? "1" : "0");
  renderChanges();
};

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
  $("propsTitle").textContent = "Properties of " + (rel === "." ? "the top folder" : rel + (res.isDir ? "/" : ""));
  renderProps();
  for (;;) {
    const r = await ask($("propsDlg"));
    if (r !== "ok") { props = null; return; }
    let set;
    try { set = propChanges(); } catch (msg) { toast(msg); continue; }
    if (!Object.keys(set).length) { props = null; return; }
    try {
      await call("SetProps", state.view.path, rel, set);
    } catch (e) { report(e); continue; } // the dialog opens again with the edits kept
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
function propChanges() {
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
    } else {
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

$("propsAdd").onchange = () => {
  const v = $("propsAdd").value;
  if (!v || !props) return;
  const custom = v === "*";
  props.rows.push({ name: custom ? "" : v, orig: null, value: "", removed: false, custom });
  renderProps();
  const last = $("propsList").lastElementChild;
  const f = last && last.querySelector(custom ? "input" : "textarea, select");
  if (f) f.focus();
};

$("propsFileBtn").onclick = () => state.diffPath && openProps(state.diffPath);
$("propsTopBtn").onclick = () => state.view && openProps(".");

// ---- Comparing ---------------------------------------------------------------------------------
//
// What differs between two places in the repository: two tags, a branch and
// trunk, or one folder (or file) at two revisions. The compare window lists
// the changed files; picking one shows its diff.

// repoLabel shows a repository URL from the repository root (^/trunk).
function repoLabel(url) {
  const root = state.info && state.info.repoRoot;
  return readable(root && url.startsWith(root) ? "^" + url.slice(root.length) : url);
}
function branchLabel(br) { return br.kind === "trunk" ? "trunk" : `${br.kind} ${br.name}`; }

// openCompare asks what to compare from ({url, rev (0: the latest), label}) with.
async function openCompare(from) {
  const isFile = !!(state.historyFile && from.url === state.historyFile.url);
  if (!isFile && state.view && state.view.type === "wc" && !state.branches) await loadBranches();
  const opts = [];
  if (from.rev) opts.push({ value: "head", label: `The latest ${repoLabel(from.url)}`, to: { url: from.url, rev: 0, label: `the latest ${repoLabel(from.url)}` } });
  opts.push({ value: "rev", label: `Another revision of ${repoLabel(from.url)}…` });
  if (!isFile && state.branches) {
    state.branches.branches.forEach((br, i) => {
      if (br.url === from.url) return; // itself (its latest is offered above)
      opts.push({ value: "br" + i, label: branchLabel(br) + (br.current ? " (your working copy is on it)" : ""), to: { url: br.url, rev: 0, label: branchLabel(br) } });
    });
  }
  $("cmpFrom").textContent = `Compare ${from.label} with:`;
  $("cmpWith").replaceChildren(...opts.map(o => el("option", { value: o.value }, o.label)));
  const showRev = () => { $("cmpRevRow").hidden = $("cmpWith").value !== "rev"; };
  $("cmpWith").onchange = () => { showRev(); if (!$("cmpRevRow").hidden) $("cmpRev").focus(); };
  $("cmpRev").value = "";
  showRev();
  for (;;) {
    const r = ask($("cmpPickDlg"));
    $("cmpWith").focus();
    if (await r !== "ok") return;
    const pick = opts.find(o => o.value === $("cmpWith").value);
    let to = pick && pick.to;
    if ($("cmpWith").value === "rev") {
      const n = Number($("cmpRev").value.trim().replace(/^r/i, ""));
      if (!Number.isInteger(n) || n < 1) { toast("Type a revision number, like 120."); continue; }
      to = { url: from.url, rev: n, label: `r${n} of ${repoLabel(from.url)}` };
    }
    if (!to) return;
    // One place at two revisions: older first, so additions show as added.
    if (to.url === from.url && from.rev && to.rev && to.rev < from.rev) return showCompare(to, from);
    return showCompare(from, to);
  }
}

let cmp = null; // on show: {a, b, changes}

async function showCompare(a, b) {
  cmp = { a, b, changes: null };
  $("compareTitle").textContent = `${a.label}  →  ${b.label}`;
  $("compareTitle").title = `Changes from ${a.label} to ${b.label}`;
  $("compareStats").textContent = "";
  $("compareList").replaceChildren(el("tr", {}, el("td", { class: "placeholder" }, "Asking the server…")));
  $("compareDiff").replaceChildren();
  if (!$("compareDlg").open) ask($("compareDlg")).then(() => { cmp = null; });
  try {
    const ch = await call("Compare", a.url, a.rev, b.url, b.rev);
    if (!cmp || cmp.a !== a) return;
    cmp.changes = ch;
    renderCompare();
  } catch (e) {
    if (cmp && cmp.a === a) $("compareList").replaceChildren(el("tr", {}, el("td", { class: "placeholder" }, parseErr(e).message)));
  }
}

function renderCompare() {
  const tb = $("compareList");
  tb.replaceChildren();
  const files = cmp.changes;
  $("compareStats").textContent = files.length === 1 ? "1 change" : files.length + " changes";
  if (!files.length) {
    tb.append(el("tr", {}, el("td", { class: "placeholder" }, "No differences.")));
    $("compareDiff").replaceChildren();
    return;
  }
  for (const c of files) {
    const letter = { added: "A", deleted: "D", modified: "M", replaced: "R" }[c.item] || (c.props === "modified" ? "P" : "");
    const cls = { added: "added", deleted: "deleted", modified: "modified", replaced: "replaced" }[c.item] || "props";
    const name = c.path || cmp.a.label;
    const tr = el("tr", { onclick: () => showCompareDiff(c) },
      el("td", {}, el("span", { class: "st st-" + cls, title: c.item === "none" ? "properties changed" : c.item }, letter)),
      el("td", { class: "path", title: name }, name + (c.kind === "dir" ? "/" : "")));
    tr.dataset.key = c.path;
    tr._move = () => showCompareDiff(c);
    tb.append(tr);
  }
  rovingRows(tb, false);
  showCompareDiff(files[0]);
}

async function showCompareDiff(c) {
  const at = cmp;
  for (const tr of $("compareList").children) tr.classList.toggle("sel", tr.dataset.key === c.path);
  if (c.kind === "dir" && c.item !== "none" && c.item !== "modified") {
    $("compareDiff").replaceChildren(el("div", { class: "placeholder" }, `Folder ${c.item}.`));
    return;
  }
  $("compareDiff").replaceChildren(el("div", { class: "placeholder" }, "Loading…"));
  at.sel = c.path;
  try {
    const d = await call("CompareDiff", at.a.url, at.a.rev, at.b.url, at.b.rev, c.path, c.item);
    if (cmp === at && at.sel === c.path) renderDiff($("compareDiff"), d);
  } catch (e) {
    if (cmp === at && at.sel === c.path) $("compareDiff").replaceChildren(el("div", { class: "placeholder" }, parseErr(e).message));
  }
}
$("compareSwap").onclick = () => { if (cmp) showCompare(cmp.b, cmp.a); };

// tagAt makes a tag of the working copy's trunk or branch as it was at rev.
async function tagAt(rev) {
  if (!state.branches) await loadBranches();
  if (!state.branches) return;
  if (!state.branches.layout.kind) return toast("This repository doesn't use trunk / branches / tags folders, so TrunkLine doesn't know where tags go.");
  await newBranch("tag", rev);
}

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

// ---- Working copy buttons ----------------------------------------------------------------

// ---- Incoming changes: "Update (3)" ----------------------------------------------------------

const INCOMING_EVERY = 5 * 60 * 1000;
let lastIncomingCheck = 0;

let incomingShown = 0;
function setIncoming(n) {
  const b = $("updateBtn");
  const what = `${n} file${n === 1 ? "" : "s"} changed on the server since your last update`;
  // The badge is made anew only when the count goes up, so it pops in then and not on every check.
  if (!n) b.replaceChildren("Update");
  else if (n > incomingShown || !b.querySelector(".badge")) b.replaceChildren("Update", el("span", { class: "badge", "aria-hidden": "true" }, String(n)));
  else b.querySelector(".badge").textContent = String(n);
  incomingShown = n;
  b.title = n ? what + ": svn update" : "svn update";
  b.setAttribute("aria-label", n ? `Update (${what})` : "Update");
  b.classList.toggle("has-incoming", n > 0);
}

// checkIncoming asks the server, in the background, what an update would
// bring for the open working copy. It never shows a login or an error (a
// background check mustn't interrupt): on failure the badge stays as it was.
async function checkIncoming() {
  if (!state.view || state.view.type !== "wc" || document.hidden) return;
  const wc = state.view.path;
  lastIncomingCheck = Date.now();
  try {
    const n = await Go().Incoming(wc);
    if (state.view && state.view.path === wc) setIncoming(n);
  } catch { /* offline, or needs a login: try again later */ }
}
setInterval(checkIncoming, INCOMING_EVERY);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && Date.now() - lastIncomingCheck > 60 * 1000) checkIncoming();
});

// conflictsAfter tells the user plainly when an update or switch left
// conflicts (svn finishes "successfully" either way), and shows them.
function conflictsAfter(what) {
  const n = state.changes.filter(c => c.item === "conflicted" || c.props === "conflicted" || c.treeConflict).length;
  if (!n) return false;
  $("progDlg").close();
  setTab("changes");
  setBanner("conflict", `${what}, but ${n} file${n === 1 ? "" : "s"} ${n === 1 ? "has a conflict" : "have conflicts"} between your ` +
    "changes and the incoming ones (marked C). Pick each one, look at its diff, then resolve it below before committing.");
  return true;
}

$("updateBtn").onclick = () => busy($("updateBtn"), async () => {
  const wc = state.view.path;
  try {
    await withProgress("Updating " + $("wcName").textContent, () => call("Update", wc));
  } finally {
    state.log = [];
    await refreshWc(state.incoming);
    conflictsAfter("Updated");
    checkIncoming();
  }
});
$("checkBtn").onclick = () => busy($("checkBtn"), () => refreshWc(true));
$("refreshBtn").onclick = () => busy($("refreshBtn"), () => refreshWc(state.incoming));
$("openWcBtn").onclick = () => Go().Open(state.view.path, "");
$("cleanupBtn").onclick = () => busy($("cleanupBtn"), async () => {
  await call("Cleanup", state.view.path);
  toast("Cleanup done.", "ok");
  await refreshWc(false);
});
$("removeWcBtn").onclick = async () => {
  const wc = state.wcs.find(w => w.path === state.view.path);
  if (!await confirmDanger("Remove from the list?", `${wc ? wc.name : ""} (${state.view.path}) leaves the sidebar. The files stay on disk.`, "Remove")) return;
  await Go().RemoveWorkingCopy(state.view.path);
  state.view = null;
  store("last", "");
  await reloadLists();
  showView("welcome");
};
$("browseWcBtn").onclick = () => {
  if (state.info) openBrowse(state.info.repoRoot, state.info.url, null);
};

function refreshView() {
  if (state.view && state.view.type === "wc") refreshWc(state.incoming);
  else if (state.view && state.view.type === "browse") listBrowse();
}

// ---- Tabs and history ------------------------------------------------------------------------

const TABS = ["changes", "history", "branches", "locks"];
function currentTab() { const t = store("tab"); return TABS.includes(t) ? t : "changes"; }

function setTab(tab) {
  store("tab", tab);
  for (const b of document.querySelectorAll(".tabs [role=tab]")) {
    const on = b.dataset.tab === tab;
    b.setAttribute("aria-selected", on ? "true" : "false");
    b.tabIndex = on ? 0 : -1; // one Tab stop for the tab row; arrow keys move within it
  }
  for (const t of TABS) $("tab-" + t).hidden = tab !== t;
  if (tab === "history" && !state.log.length && state.info) loadLog(true);
  if (tab === "locks" && state.view && state.view.type === "wc") loadLocks();
  if (tab === "branches" && state.view && state.view.type === "wc") loadBranches();
}
for (const b of document.querySelectorAll(".tabs [role=tab]")) b.onclick = () => setTab(b.dataset.tab);

// Arrow keys, Home and End move between the tabs; Enter or Space (a click)
// opens one. Opening asks the server for History, Branches and Locks, so
// focus moving past a tab doesn't open it.
document.querySelector(".tabs").addEventListener("keydown", e => {
  const tabs = [...document.querySelectorAll(".tabs [role=tab]")];
  const i = tabs.indexOf(document.activeElement);
  if (i < 0) return;
  const to = { ArrowRight: (i + 1) % tabs.length, ArrowLeft: (i - 1 + tabs.length) % tabs.length, Home: 0, End: tabs.length - 1 }[e.key];
  if (to === undefined) return;
  e.preventDefault();
  for (const t of tabs) t.tabIndex = t === tabs[to] ? 0 : -1;
  tabs[to].focus();
});
// Ctrl+1..4 open the tabs from anywhere in a working copy.
document.addEventListener("keydown", e => {
  if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey || modalOpen() || !state.view || state.view.type !== "wc") return;
  const tab = TABS[Number(e.key) - 1];
  if (!tab) return;
  e.preventDefault();
  setTab(tab);
  document.getElementById("tabbtn-" + tab).focus();
});

const LOG_PAGE = 100;

// What the History tab shows: the working copy's history, or one file's
// (historyFile: {url, label}), optionally filtered by a search.
state.historyFile = null;
state.logSearch = "";
const SEARCH_LIMIT = 500;

function logTarget() { return state.historyFile ? state.historyFile.url : state.info.url; }

async function loadLog(fresh) {
  if (!state.info) return;
  const url = logTarget(), search = state.logSearch;
  const from = fresh || !state.log.length ? 0 : state.log[state.log.length - 1].revision - 1;
  if (!fresh && from < 1) return;
  showLogStatus(search ? "Searching all of the history…" : "");
  try {
    const entries = await call("Log", url, from, search ? SEARCH_LIMIT : LOG_PAGE, search);
    if (!state.info || logTarget() !== url || state.logSearch !== search) return;
    state.log = fresh ? entries : state.log.concat(entries);
    if (fresh) { state.logRev = null; $("revDetail").hidden = true; $("revDiff").replaceChildren(); }
    renderLog();
    $("moreLog").hidden = search ? true : entries.length < LOG_PAGE;
    showLogStatus(search ? (entries.length >= SEARCH_LIMIT ? `The newest ${SEARCH_LIMIT} matches for "${search}"`
      : `${entries.length} match${entries.length === 1 ? "" : "es"} for "${search}"`) : "");
  } catch (e) { showLogStatus(""); report(e); }
}
$("moreLog").onclick = () => loadLog(false);

function showLogStatus(text) {
  const filtered = !!(state.historyFile || state.logSearch);
  $("logFile").hidden = !state.historyFile;
  if (state.historyFile) $("logFile").textContent = "History of " + state.historyFile.label;
  $("logStatus").hidden = !text && !filtered;
  $("logStatusText").textContent = text;
  $("logShowAll").hidden = !filtered;
}

$("logSearch").addEventListener("keydown", e => {
  if (e.key !== "Enter") return;
  state.logSearch = $("logSearch").value.trim();
  state.log = [];
  loadLog(true);
});
$("logSearch").addEventListener("search", () => { // the clear (x) button
  if ($("logSearch").value === "" && state.logSearch) { state.logSearch = ""; state.log = []; loadLog(true); }
});
$("logShowAll").onclick = () => {
  state.historyFile = null;
  state.logSearch = "";
  $("logSearch").value = "";
  state.log = [];
  loadLog(true);
};

// showFileHistory opens the History tab on one working copy file.
function showFileHistory(rel) {
  state.historyFile = { url: state.info.url.replace(/\/+$/, "") + "/" + encodePath(rel), label: rel };
  state.logSearch = "";
  $("logSearch").value = "";
  state.log = [];
  setTab("history");
  loadLog(true);
}

function renderLog() {
  const tb = $("log");
  const hadFocus = tb.contains(document.activeElement);
  tb.replaceChildren();
  for (const e of state.log) {
    const first = (e.message || "").split("\n")[0];
    const tr = el("tr", { class: e.revision === state.logRev ? "sel" : "", onclick: () => showRevision(e) },
      el("td", { class: "muted" }, "r" + e.revision),
      el("td", {}, e.author || ""),
      el("td", { class: "muted" }, fmtDate(e.date)),
      el("td", { class: "msg", title: e.message }, first));
    tr.dataset.key = String(e.revision);
    tr._move = tr._enter = () => showRevision(e);
    tb.append(tr);
  }
  if (!state.log.length) tb.append(el("tr", {}, el("td", { class: "placeholder" }, "No history")));
  rovingRows(tb, hadFocus);
}

function showRevision(e) {
  state.logRev = e.revision;
  renderLog();
  const root = state.info.repoRoot;
  const d = $("revDetail");
  d.hidden = false;
  const paths = el("div", { class: "paths" });
  const pick = (div, url, rev) => {
    for (const x of paths.children) x.classList.remove("sel");
    if (div) div.classList.add("sel");
    $("revDiff").replaceChildren(el("div", { class: "placeholder" }, "Loading…"));
    call("RevisionDiff", url, rev)
      .then(t => { if (state.logRev === rev) renderDiff($("revDiff"), t); })
      .catch(err => $("revDiff").replaceChildren(el("div", { class: "placeholder" }, parseErr(err).message)));
  };
  for (const p of e.paths) {
    // A deleted path only exists in the revision before.
    const peg = p.action === "D" ? e.revision - 1 : e.revision;
    const div = el("div", {},
      el("span", { class: "st st-" + ({ A: "added", D: "deleted", M: "modified", R: "replaced" }[p.action] || "") }, p.action),
      " " + p.path + (p.copyFrom ? `  (from ${p.copyFrom}@${p.copyRev})` : ""));
    if (p.kind !== "dir") div.onclick = () => pick(div, root + encodePath(p.path) + "@" + peg, e.revision);
    else div.classList.add("muted");
    paths.append(div);
  }
  d.replaceChildren(
    el("div", {}, el("b", {}, "r" + e.revision), "  ", e.author || "", el("span", { class: "muted" }, "  " + fmtDate(e.date))),
    el("pre", {}, e.message || "(no message)"),
    el("div", { class: "row" },
      el("button", { onclick: () => pick(null, root + "@" + e.revision, e.revision) }, "Show all changes"),
      " ",
      el("button", { title: "Compare this revision with another, the latest, or a branch or tag",
        onclick: () => flow(() => openCompare({ url: logTarget(), rev: e.revision, label: `r${e.revision} of ${repoLabel(logTarget())}` })) }, "Compare…"),
      " ",
      el("button", { title: "Make a tag (e.g. a release) of the code as it was at this revision",
        onclick: () => flow(() => tagAt(e.revision)) }, "Tag this revision…"),
      " ",
      el("button", { class: "danger", title: "Take this commit's changes back out of your working copy, to review and commit",
        onclick: () => flow(() => undoRevision(e)) }, "Undo this commit")),
    paths);
  $("revDiff").replaceChildren(el("div", { class: "placeholder" }, "Pick a file to see what this revision changed in it."));
}

// ---- Repository browser ------------------------------------------------------------------------

// openBrowse shows url; server is the saved repository it came from (null
// when browsing from a working copy).
async function openBrowse(server, url, name) {
  state.view = { type: "browse", server: name === null ? null : server, url, root: null, name };
  if (name !== null) store("last", JSON.stringify(state.view));
  $("browseName").textContent = name || "Repository";
  $("removeServerBtn").hidden = name === null;
  renderSidebar();
  showView("browse");
  $("entries").replaceChildren(el("tr", {}, el("td", { class: "placeholder", colspan: 5 }, "Loading…")));
  try {
    const info = await call("RepoInfo", url);
    if (state.view.url !== url) return;
    state.view.root = info.repoRoot;
    if (name === null) $("browseName").textContent = info.repoRoot.split("/").pop();
  } catch (e) {
    report(e);
    $("entries").replaceChildren();
    return;
  }
  listBrowse();
}

function renderCrumbs() {
  const { root, url } = state.view;
  const c = $("crumbs");
  c.replaceChildren();
  const rest = url.startsWith(root) ? url.slice(root.length).split("/").filter(Boolean) : [];
  const go = u => () => { state.view.url = u; listBrowse(); };
  c.append(el("button", { class: "ghost", onclick: go(root) }, decodeURIComponent(root.split("/").pop() || root)));
  let u = root;
  for (const part of rest) {
    u += "/" + part;
    c.append(el("span", { class: "muted" }, "/"), el("button", { class: "ghost", onclick: go(u) }, decodeURIComponent(part)));
  }
}

async function listBrowse() {
  const url = state.view.url;
  renderCrumbs();
  try {
    const entries = await call("List", url);
    if (state.view.url !== url) return;
    const tb = $("entries");
    const hadFocus = tb.contains(document.activeElement);
    tb.replaceChildren();
    for (const e of entries) {
      const dir = e.kind === "dir";
      const tr = el("tr", {},
        el("td", {}, el("span", { class: dir ? "" : "muted" }, (dir ? "▸ " : "  ") + e.name + (dir ? "/" : "")),
          e.lock ? lockMark("other", e.lock) : null),
        el("td", { class: "muted" }, e.commit.revision ? "r" + e.commit.revision : ""),
        el("td", {}, e.commit.author || ""),
        el("td", { class: "muted" }, fmtDate(e.commit.date)),
        el("td", { class: "muted" }, dir ? "" : fmtSize(e.size)));
      tr.style.cursor = "pointer";
      const child = url.replace(/\/+$/, "") + "/" + encodeURIComponent(e.name);
      if (dir) {
        tr.onclick = () => { state.view.url = child; listBrowse(); };
      } else {
        tr.title = "Click to see who changed each line (blame)";
        tr.onclick = () => openBlame(() => call("BlameURL", child));
      }
      tr.dataset.key = e.name;
      tr._enter = tr.onclick;
      tb.append(tr);
    }
    if (!entries.length) tb.append(el("tr", {}, el("td", { class: "placeholder", colspan: 5 }, "Empty folder")));
    tb._lastKey = null; // a new folder: start at its first row
    rovingRows(tb, hadFocus);
  } catch (e) { report(e); }
}

$("removeServerBtn").onclick = async () => {
  if (!state.view.server) return;
  if (!await confirmDanger("Remove repository?", `${state.view.name} leaves the sidebar. Nothing changes on the server.`, "Remove")) return;
  await Go().RemoveServer(state.view.server);
  state.view = null;
  store("last", "");
  await reloadLists();
  showView("welcome");
};

// ---- Checkout -----------------------------------------------------------------------------------

function coTarget() {
  const parent = $("coParent").value.trim().replace(/[\\/]+$/, "");
  const name = $("coName").value.trim();
  return parent && name ? parent + sep() + name : "";
}
function showCoTarget() {
  const t = coTarget();
  $("coTarget").textContent = t ? "Checks out into " + t : "Pick a folder and a name.";
}
$("coParent").oninput = showCoTarget;
$("coName").oninput = showCoTarget;
$("coPick").onclick = async () => {
  const d = await Go().PickFolder("Check out into…");
  if (d) { $("coParent").value = d; showCoTarget(); }
};

// folderName suggests a local folder name for a repository URL: trunk or a
// branch is named after its project.
function folderName(url) {
  const parts = url.split("/").filter(Boolean).map(decodeURIComponent);
  let name = parts[parts.length - 1] || "checkout";
  if ((name === "trunk" || parts[parts.length - 2] === "branches") && parts.length >= 2) {
    const proj = parts[parts.length - (name === "trunk" ? 2 : 3)];
    if (proj) name = name === "trunk" ? proj : proj + "-" + name;
  }
  return name;
}

// askTarget opens the checkout dialog (also used for export) and resolves
// with the chosen local folder, or "" if canceled.
async function askTarget(title, note, okLabel) {
  const url = state.view.url;
  $("coTitle").textContent = title;
  $("coNote").textContent = note;
  $("coOk").textContent = okLabel;
  $("coUrl").value = url;
  $("coParent").value = store("checkoutParent") || "";
  $("coName").value = folderName(url);
  showCoTarget();
  if (await ask($("checkoutDlg")) !== "ok") return "";
  const target = coTarget();
  if (!target) { toast("Pick a folder and a name.", "err"); return ""; }
  store("checkoutParent", $("coParent").value.trim());
  return target;
}

$("checkoutHereBtn").onclick = async () => {
  const target = await askTarget("Check out", "A working copy: you can change files and commit them back to this repository.", "Check out");
  if (!target) return;
  try {
    const wc = await withProgress("Checking out " + $("coName").value, () => call("Checkout", $("coUrl").value.trim(), target));
    $("progDlg").close();
    await reloadLists();
    toast("Checked out into " + wc.path, "ok");
    openWc(wc.path);
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
  }
};

$("exportHereBtn").onclick = async () => {
  const target = await askTarget("Export",
    "Just the files: a plain copy with no link to this repository. You can't commit from it, and nothing you do to it touches the repository.",
    "Export");
  if (!target) return;
  try {
    await withProgress("Exporting " + $("coName").value, () => call("Export", $("coUrl").value.trim(), target));
    $("progDlg").close();
    toast("Exported into " + target, "ok");
    Go().Open(target, "");
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
  }
};

// ---- Import -------------------------------------------------------------------------------------

function imTarget() {
  const name = $("imName").value.trim().replace(/^\/+|\/+$/g, "");
  return name ? state.view.url.replace(/\/+$/, "") + "/" + encodePath(name) : "";
}
function showImTarget() {
  const t = imTarget();
  $("imTarget").textContent = t ? "Creates " + decodeURI(t) : "Name the new repository folder.";
}
$("imName").oninput = showImTarget;
$("imPick").onclick = async () => {
  const d = await Go().PickFolder("Folder to import");
  if (!d) return;
  $("imDir").value = d;
  const base = d.split(/[\\/]/).filter(Boolean).pop() || "";
  if (!$("imName").value.trim()) $("imName").value = base;
  if (!$("imMsg").value.trim()) $("imMsg").value = "Import " + base;
  showImTarget();
};

$("importHereBtn").onclick = async () => {
  $("imDir").value = "";
  $("imName").value = "";
  $("imMsg").value = "";
  showImTarget();
  if (await ask($("importDlg")) !== "ok") return;
  const dir = $("imDir").value.trim(), url = imTarget(), msg = $("imMsg").value.trim();
  if (!dir || !url) return toast("Pick a folder and name the new repository folder.", "err");
  if (!msg) return toast("Write a commit message.", "err");
  const inPlace = $("imInPlace").checked;
  try {
    const rev = await withProgress("Importing " + dir, () => call("Import", dir, url, msg, inPlace));
    $("progDlg").close();
    toast(`Imported as revision ${rev}.` + (inPlace ? ` ${dir} is now a working copy.` : ""), "ok");
    if (inPlace) {
      await reloadLists();
      // svn reports the working copy by its absolute path, which may be spelled differently
      const trim = p => p.replace(/[\\/]+$/, "");
      const wc = state.wcs.find(w => trim(w.path) === trim(dir)) || state.wcs[state.wcs.length - 1];
      if (wc) openWc(wc.path);
    } else {
      listBrowse();
    }
  } catch (e) {
    if (parseErr(e).kind !== "canceled") report(e);
    await reloadLists();
    if (state.view && state.view.type === "browse") listBrowse();
  }
};

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
    (info.canInstall ? "" : "  (built from source: pull and rebuild to update)");
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
  if (info.available) return info.canInstall ? `${info.latest} is available` : `${info.latest} is available: pull and rebuild`;
  if (info.note) return info.note + (info.latest ? ` (latest release: ${info.latest})` : "");
  return info.latest ? `Up to date (latest release: ${info.latest})` : "No releases published yet";
}

async function openAbout() {
  const s = state.startup;
  const kind = await Go().InstallKind();
  $("aboutVersion").textContent = `${s.version} (${{ deb: ".deb package", windows: "Windows setup", source: "built from source" }[kind] || kind})`;
  $("aboutSvn").textContent = s.svnError ? "not found" : `${s.svnVersion} (${s.svnPath})`;
  if (!$("aboutDlg").open) ask($("aboutDlg"));
}
$("version").onclick = openAbout;
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
  "switchUrlBtn", "lockBtn", "lockPickBtn", "unlockBtn", "updateInstall", "updateBtn", "savePatchBtn", "applyPatchBtn"]) {
  const handler = $(id).onclick;
  if (handler) $(id).onclick = e => flow(() => handler(e));
}

// ---- Start ------------------------------------------------------------------------------------

// ---- Look: theme and font ---------------------------------------------------------------------
//
// The choice is saved in the settings file (SetLook) and copied to
// localStorage, which the script in index.html's <head> reads to apply it
// before anything paints.

const ZOOMS = [50, 67, 75, 80, 90, 100, 110, 125, 150, 175, 200];
const look = { theme: "system", font: "mono", zoom: 100 };
$("lookZoom").append(...ZOOMS.map(z => el("option", { value: z }, z + "%")));

function applyLook() {
  const root = document.documentElement;
  if (look.theme === "dark" || look.theme === "light") root.dataset.theme = look.theme; else delete root.dataset.theme;
  if (look.font === "regular") root.dataset.font = look.font; else delete root.dataset.font;
  root.style.zoom = look.zoom === 100 ? "" : String(look.zoom / 100);
  store("theme", look.theme);
  store("font", look.font);
  store("zoom", String(look.zoom));
  $("lookTheme").value = look.theme;
  $("lookFont").value = look.font;
  $("lookZoom").value = String(look.zoom);
  syncWindowColour();
}

// setLook changes some of theme, font and zoom, shows it, and saves it.
async function setLook(change) {
  Object.assign(look, change);
  applyLook();
  try { await call("SetLook", look.theme, look.font, look.zoom); } catch (e) { report(e); }
}

// stepZoom moves one step up (1) or down (-1) the zoom levels.
function stepZoom(dir) {
  const i = ZOOMS.findIndex(z => z >= look.zoom);
  const next = ZOOMS[Math.min(ZOOMS.length - 1, Math.max(0, (i < 0 ? ZOOMS.length - 1 : i) + dir))];
  if (next !== look.zoom) setLook({ zoom: next });
  toast(`Size ${next}%`);
}

// syncWindowColour gives the window behind the page (seen while it opens or
// resizes) the page's background.
function syncWindowColour() {
  const m = getComputedStyle(document.body).backgroundColor.match(/\d+/g);
  if (m && window.runtime && window.runtime.WindowSetBackgroundColour) window.runtime.WindowSetBackgroundColour(+m[0], +m[1], +m[2], 255);
}
matchMedia("(prefers-color-scheme: light)").addEventListener("change", syncWindowColour);

$("lookTheme").onchange = () => setLook({ theme: $("lookTheme").value });
$("lookFont").onchange = () => setLook({ font: $("lookFont").value });
$("lookZoom").onchange = () => setLook({ zoom: Number($("lookZoom").value) });

function openPrefs() {
  applyLook();
  $("prefsAuto").checked = state.startup.updateCheck;
  if (!$("prefsDlg").open) ask($("prefsDlg"));
}

// ---- Keyboard shortcuts ------------------------------------------------------------------------

const SHORTCUTS = [
  ["Anywhere"],
  ["Alt+F, Alt+V, Alt+H", "Open the File, View or Help menu (then ← → ↑ ↓)"],
  ["Ctrl+,", "Preferences"],
  ["F1", "This list"],
  ["F5", "Refresh"],
  ["Ctrl+= / Ctrl+− / Ctrl+0", "Bigger, smaller, actual size"],
  ["Esc", "Close a dialog, menu or message"],
  ["Ctrl+Q", "Quit"],
  ["In a working copy"],
  ["Ctrl+1 … Ctrl+4", "Changes, History, Branches, Locks"],
  ["Ctrl+Enter", "Commit (in the message box)"],
  ["In a list"],
  ["↑ ↓ Home End", "Move (shows the diff or the revision)"],
  ["Space", "Tick or untick"],
  ["Enter", "Open the file"],
];
function showKeys() {
  $("keysList").replaceChildren(...SHORTCUTS.map(([k, what]) => what
    ? el("tr", {}, el("td", {}, k), el("td", {}, what))
    : el("tr", { class: "head" }, el("td", { colspan: 2 }, k))));
  if (!$("keysDlg").open) ask($("keysDlg"));
}

// ---- Menu commands and their shortcuts --------------------------------------------------------

async function quitApp() {
  if ((inFlight > 0 || flowRunning) && !await confirmDanger("Quit while svn is working?",
    "An svn command is still running. Quitting now stops it, which can leave the working copy needing a clean up.", "Quit")) return;
  window.runtime.Quit();
}

async function openRepoPage(path) {
  try { await Go().OpenReleasePage(await Go().RepoURL() + path); } catch (e) { report(e); }
}

const commands = {
  addWc: () => $("addWcBtn").onclick(),
  addServer: () => $("addServerBtn").onclick(),
  prefs: openPrefs,
  quit: quitApp,
  tab: b => { if (state.view && state.view.type === "wc") setTab(b.dataset.tab); },
  unversioned: () => $("showUnversioned").click(),
  sbs: () => setSideBySide(!sideBySide()),
  refresh: refreshView,
  theme: b => setLook({ theme: b.dataset.theme }),
  zoomIn: () => stepZoom(1),
  zoomOut: () => stepZoom(-1),
  zoomReset: () => { setLook({ zoom: 100 }); toast("Size 100%"); },
  keys: showKeys,
  checkUpdate: async () => { await openAbout(); $("aboutCheck").click(); },
  releases: () => openRepoPage("/releases"),
  issue: () => openRepoPage("/issues/new"),
  github: () => openRepoPage(""),
  about: openAbout,
};

document.addEventListener("keydown", e => {
  const ctrl = (e.ctrlKey || e.metaKey) && !e.altKey;
  let cmd = null;
  if (e.key === "F1" && !modalOpen()) cmd = "keys";
  else if (e.key === "F5" && !modalOpen()) cmd = "refresh";
  else if (ctrl && (e.key === "=" || e.key === "+")) cmd = "zoomIn";
  else if (ctrl && (e.key === "-" || e.key === "_")) cmd = "zoomOut";
  else if (ctrl && e.key === "0") cmd = "zoomReset";
  else if (ctrl && !e.shiftKey && e.key === "," && !modalOpen()) cmd = "prefs";
  else if (ctrl && !e.shiftKey && e.key.toLowerCase() === "q") cmd = "quit";
  if (!cmd) return;
  e.preventDefault();
  commands[cmd]();
});

async function start() {
  const s = await Go().Startup();
  state.startup = s;
  Object.assign(look, { theme: s.theme, font: s.font, zoom: s.zoom });
  applyLook();
  state.wcs = s.workingCopies;
  state.servers = s.servers;
  $("version").textContent = s.version;
  if (s.svnError) {
    $("svnChip").hidden = false;
    const win = s.os === "windows";
    $("noSvn").textContent = "The svn command-line client was not found: " + s.svnError + ". " + (win
      ? "Install TortoiseSVN and tick \"command line client tools\" in its installer (or install SlikSVN), then restart TrunkLine."
      : "Install it (Ubuntu: sudo apt install subversion), then restart TrunkLine.");
    $("noSvn").hidden = false;
  } else {
    $("svnChip").hidden = true;
  }
  $("showUnversioned").checked = store("showUnversioned") !== "0";
  renderSidebar();

  let last = null;
  try { last = JSON.parse(store("last") || "null"); } catch { /* ignore */ }
  if (last && last.type === "wc" && state.wcs.some(w => w.path === last.path)) openWc(last.path);
  else if (last && last.type === "browse" && state.servers.some(x => x.url === last.server)) openBrowse(last.server, last.server, last.name);
  else showView("welcome");
}

start();
