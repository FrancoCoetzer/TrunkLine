// Shared by the specs: the fixture, opening its working copy, the console
// error check, and running the command line against the running app.
const fs = require("fs");
const path = require("path");
const { execFile } = require("child_process");
const { expect } = require("@playwright/test");

const fx = () => JSON.parse(fs.readFileSync(path.join(__dirname, ".fixture.json"), "utf8"));

// Errors that aren't ours: Wails' ipc.js trips when the page is opened in a
// browser instead of the app's window, and there's no favicon.
const KNOWN = [/reading 'nodes'/, /favicon/, /status of 404/];

function watchErrors(page) {
  const errors = [];
  page.on("pageerror", e => errors.push(String(e)));
  page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
  return () => expect(errors.filter(e => !KNOWN.some(k => k.test(e)))).toEqual([]);
}

// openWorkingCopy: add the fixture's working copy and open it, as the
// sidebar's Add would (without the folder picker).
async function openWorkingCopy(page, theme = "light") {
  const { wc } = fx();
  await page.goto("/");
  await page.evaluate(async ([wc, theme]) => {
    await window.go.main.App.AddWorkingCopy(wc);
    await reloadLists();
    look.theme = theme;
    applyLook();
    await openWc(wc);
  }, [wc, theme]);
  await expect(page.locator("#changeCount")).toContainText("change");
  return wc;
}

// trunkline: a second launch of the app with these arguments, from the
// working copy. It hands them to the running app and exits.
function trunkline(...args) {
  const f = fx();
  return new Promise(resolve => execFile(f.bin, ["-assetdir", f.assetdir, ...args],
    { cwd: f.wc, env: { ...process.env, ...f.env }, timeout: 15_000 }, () => resolve()));
}

async function shot(page, testInfo, name) {
  // Finished animations, so a dialog isn't caught fading in.
  await testInfo.attach(name, { body: await page.screenshot({ animations: "disabled" }), contentType: "image/png" });
}

module.exports = { fx, watchErrors, openWorkingCopy, trunkline, shot };
