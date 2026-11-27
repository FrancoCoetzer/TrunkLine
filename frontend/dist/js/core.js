"use strict";
// TrunkLine's page logic, split by area into the scripts in this folder.
// index.html loads them in order as classic scripts (not modules), so they
// share one global scope: this file's state, $ and helpers are used by all.
// Code that runs as a file loads may only use what earlier files define.
// Every svn call goes through call(), which turns the Go side's JSON errors
// into a login or certificate dialog and retries.

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
function setupMenu(menu, defaultBtn, align) {
  const anchor = () => menu._btn || defaultBtn; // a menu shared by many buttons sets _btn
  menu.addEventListener("beforetoggle", e => {
    if (e.newState !== "open") return;
    if (menu._prepare) menu._prepare();
    const btn = anchor();
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
    const btn = anchor();
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
setupMenu($("entryMenu"), $("repoCopyUrlBtn"), "right"); // opened from each row's ⋯ (its _btn)

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
