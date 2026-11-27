// Opened from outside: a second launch hands its action to the running app,
// whose page (this one too) starts it; a folder dropped on the window opens
// its working copy.
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { test, expect } = require("@playwright/test");
const { fx, watchErrors, openWorkingCopy, trunkline, shot } = require("./helpers");

let noErrors;
test.beforeEach(async ({ page }) => {
  noErrors = watchErrors(page);
  await openWorkingCopy(page);
  await page.getByRole("tab", { name: "Branches" }).click(); // start away from Changes
});
test.afterEach(() => noErrors());

test("commit ticks the file and waits for a message", async ({ page }, info) => {
  await trunkline("commit", "src/motor.c");
  await expect(page.getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => page.evaluate(() => [...state.checked])).toEqual(["src/motor.c"]);
  await expect(page.locator("#diff")).toContainText('#include "encoder.h"');
  await shot(page, info, "cli-commit");
});

test("log opens the file's history", async ({ page }, info) => {
  await trunkline("log", "src/motor.c");
  await expect(page.locator("#log")).toContainText("Limit the speed");
  await expect(page.locator("#log")).not.toContainText("Project layout"); // not in this file's history
  await shot(page, info, "cli-log");
});

test("blame opens for a file", async ({ page }, info) => {
  await trunkline("blame", "src/motor.c");
  await expect(page.locator("#blameDlg")).toBeVisible();
  await expect(page.locator("#blameDlg")).toContainText("not committed");
  await shot(page, info, "cli-blame");
});

test("a path outside a working copy offers to make it one", async ({ page }) => {
  await trunkline("commit", "/");
  await expect(page.locator("#notWcDlg")).toContainText("/ is not in an SVN working copy");
  await page.keyboard.press("Escape");
  await expect(page.locator("#notWcDlg")).toBeHidden();
});

// Wails hands the page a drop's paths; openDropped is what it calls.
test("a dropped folder opens its working copy", async ({ page }) => {
  const { wc } = fx();
  await page.evaluate(() => showView("welcome"));
  await expect(page.locator("#wcView")).toBeHidden();
  await page.evaluate(p => openDropped([p]), wc + "/src");
  await expect(page.locator("#wcView")).toBeVisible();
  await expect(page.locator("#wcName")).toContainText("wc");
});

// scratch: a repository and a plain folder of the test's own, so the
// fixture's repository stays as the other tests expect it.
function scratch(name) {
  const dir = path.join(fx().dir, name);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  const repo = path.join(dir, "repo");
  execFileSync("svnadmin", ["create", repo]);
  execFileSync("svn", ["mkdir", "--parents", "-q", "-m", "Layout", "file://" + repo + "/project/trunk"]);
  const folder = path.join(dir, "plain");
  fs.mkdirSync(folder);
  fs.writeFileSync(path.join(folder, "hello.txt"), "hi\n");
  return { url: "file://" + repo, folder };
}

test("a dropped plain folder: check out into it", async ({ page }, info) => {
  const { url, folder } = scratch("co");
  await page.evaluate(p => { openDropped([p]); }, folder); // don't wait: it waits on the dialog
  const dlg = page.locator("#notWcDlg");
  await expect(dlg).toContainText(folder + " is not in an SVN working copy");
  await shot(page, info, "not-a-working-copy");
  await expect(dlg.getByRole("button", { name: "Check out into it…" })).toBeFocused(); // what Enter does
  await dlg.getByRole("button", { name: "Check out into it…" }).click();
  await expect(page.locator("#coParent")).toHaveValue(path.dirname(folder));
  await expect(page.locator("#coName")).toHaveValue("plain");
  await expect(page.locator("#coUrl")).toBeFocused();
  await page.locator("#coUrl").fill(url + "/project/trunk");
  await page.locator("#coOk").click();
  await expect(page.locator("#wcName")).toContainText("plain");
  expect(fs.existsSync(path.join(folder, ".svn"))).toBe(true);
  expect(fs.readFileSync(path.join(folder, "hello.txt"), "utf8")).toBe("hi\n"); // kept
});

test("a dropped plain folder: import it", async ({ page }) => {
  const { url, folder } = scratch("im");
  await page.evaluate(p => { openDropped([p]); }, folder); // don't wait: it waits on the dialog
  await page.locator("#notWcDlg").getByRole("button", { name: "Import it…" }).click();
  await expect(page.locator("#imDir")).toHaveValue(folder);
  await expect(page.locator("#imName")).toHaveValue("plain");
  await expect(page.locator("#imMsg")).toHaveValue("Import plain");
  await page.locator("#imBase").fill(url + "/project");
  await expect(page.locator("#imTarget")).toContainText("/project/plain");
  await page.locator("#importDlg").getByRole("button", { name: "Import", exact: true }).click();
  await expect(page.locator("#wcName")).toContainText("plain");
  expect(execFileSync("svn", ["ls", url + "/project/plain"]).toString()).toContain("hello.txt");
});

test("import from the repository browser still asks no repository", async ({ page }) => {
  await page.evaluate(() => { importFolder(""); });
  await expect(page.locator("#imBaseRow")).toBeHidden();
  await page.keyboard.press("Escape");
});

// A drag as each webview reports it: WebView2 says "Files", WebKitGTK only
// "text/uri-list" (a folder from Nautilus).
async function dragOver(page, kind) {
  return page.evaluate(kind => {
    const dataTransfer = new DataTransfer();
    if (kind === "Files") dataTransfer.items.add(new File(["x"], "x.txt"));
    else dataTransfer.setData("text/uri-list", "file:///tmp/wc\r\n");
    const over = new DragEvent("dragover", { dataTransfer, bubbles: true, cancelable: true });
    document.body.dispatchEvent(over);
    return over.defaultPrevented;
  }, kind);
}

for (const kind of ["Files", "text/uri-list"]) {
  test(`dragging ${kind} over the window shows the hint`, async ({ page }, info) => {
    expect(await dragOver(page, kind)).toBe(true);
    await expect(page.locator("#dropHint")).toBeVisible();
    if (kind === "Files") await shot(page, info, "drop-hint");
    await expect(page.locator("#dropHint")).toBeHidden(); // the drag left: no more dragover
  });
}

// The webview would otherwise open what was dropped as a page, in place of TrunkLine.
test("a drop never opens as a page", async ({ page }) => {
  const prevented = await page.evaluate(() => {
    const dataTransfer = new DataTransfer();
    dataTransfer.setData("text/uri-list", "https://example.com/\r\n");
    const drop = new DragEvent("drop", { dataTransfer, bubbles: true, cancelable: true });
    document.body.dispatchEvent(drop);
    return drop.defaultPrevented;
  });
  expect(prevented).toBe(true);
});
